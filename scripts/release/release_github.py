"""Bounded GitHub transport. Mutation responses are never blindly retried."""
import json
import os
from pathlib import Path
import urllib.error
import urllib.parse
import urllib.request
from release_common import require
from release_config import REPOSITORY

class SafeRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        require(urllib.parse.urlsplit(newurl).scheme == 'https', 'Insecure asset redirect')
        redirected = super().redirect_request(req, fp, code, msg, headers, newurl)
        if redirected is not None and urllib.parse.urlsplit(req.full_url).netloc != urllib.parse.urlsplit(newurl).netloc:
            redirected.remove_header('Authorization')
        return redirected


class GitHub:
    def __init__(self, token=None):
        self.token = token or os.environ['GH_TOKEN']

    def request(self, path, method='GET', data=None, raw=False):
        url = path if path.startswith('https://') else 'https://api.github.com/repos/' + REPOSITORY + path
        require(url.startswith(('https://api.github.com/repos/' + REPOSITORY + '/',
                                'https://uploads.github.com/repos/' + REPOSITORY + '/')), 'Unexpected GitHub API destination')
        headers = {'Authorization': 'Bearer ' + self.token, 'X-GitHub-Api-Version': '2022-11-28',
                   'Accept': 'application/octet-stream' if raw else 'application/vnd.github+json',
                   'User-Agent': 'iotools-release-gate'}
        if isinstance(data, Path):
            headers['Content-Type'] = 'application/octet-stream'
            body = data.read_bytes()
        elif data is not None:
            headers['Content-Type'] = 'application/json'
            body = json.dumps(data).encode()
        else:
            body = None
        # Never automatically retry mutation requests with uncertain outcomes.
        with urllib.request.build_opener(SafeRedirect()).open(urllib.request.Request(url, body, headers, method=method), timeout=180) as response:
            content = response.read()
        return content if raw else json.loads(content)

    def optional(self, path):
        try:
            return self.request(path)
        except urllib.error.HTTPError as e:
            if e.code == 404:
                return None
            raise

    def jobs(self, run_id):
        rows, page = [], 1
        while True:
            batch = self.request(f'/actions/runs/{run_id}/jobs?filter=latest&per_page=100&page={page}')['jobs']
            rows.extend(batch)
            if len(batch) < 100:
                return rows
            page += 1
