"""Unsigned source dependency inventory and build record.

These supplements request no OIDC token or signing permission and do not claim a
SLSA level, authenticated provenance or exhaustive per-binary runtime inventory.
"""
import json
import re
from pathlib import Path
from urllib.parse import quote
from release_common import ROOT, require, digest
from release_config import REPOSITORY, VERSION

METADATA_FILES={'manifest.json','SHA256SUMS','RELEASE_NOTES.zh-CN.md','source-sbom.cdx.json','build-provenance.json'}

def source_sbom(sha, root=ROOT):
    root=Path(root)
    sums={}
    for line in (root/'go.sum').read_text(encoding='utf-8').splitlines():
        name,version,value=line.split()
        sums[name,version]=value
    components=[]
    source=(root/'go.mod').read_text(encoding='utf-8')
    for name,version in re.findall(r'^\s+([\w./~-]+)\s+(v[^\s]+)',source,re.M):
        value=sums.get((name,version))
        require(value is not None, f'Missing resolved Go module checksum: {name}@{version}')
        purl='pkg:golang/'+quote(name,safe='/')+'@'+quote(version,safe='')
        components.append({'type':'library','name':name,'version':version,'purl':purl,'bom-ref':purl,
                           'properties':[{'name':'iotools:inventory-scope','value':'go.mod source module; not proof of linkage'},
                                         {'name':'go:module-h1','value':value}]})
    lock=(root/'mobile/pubspec.lock').read_text(encoding='utf-8')
    for name,block in re.findall(r'^  ([\w_]+):\n(.*?)(?=^  [\w_]+:|^sdks:|\Z)',lock,re.M|re.S):
        version=re.search(r'^    version: "([^"\n]+)"',block,re.M)
        origin=re.search(r'^    source: (\w+)',block,re.M)
        require(version and origin, 'Unsupported pub lock entry: '+name)
        purl='pkg:pub/'+name+'@'+version[1]
        row={'type':'library','name':name,'version':version[1],'bom-ref':purl,'purl':purl,
             'properties':[{'name':'iotools:inventory-scope','value':'pubspec.lock source dependency; includes development dependencies'},
                           {'name':'pub:source','value':origin[1]}]}
        if origin[1]=='hosted':
            checksum=re.search(r'^      sha256: "?([0-9a-f]{64})"?$',block,re.M)
            require(checksum,'Missing hosted pub hash: '+name)
            row['hashes']=[{'alg':'SHA-256','content':checksum[1]}]
        components.append(row)
    require(components,'Empty source SBOM')
    return {'bomFormat':'CycloneDX','specVersion':'1.6','version':1,
            'metadata':{'component':{'type':'application','name':'iotools','version':VERSION,
                        'externalReferences':[{'type':'vcs','url':f'https://github.com/{REPOSITORY}/tree/{sha}'}]},
                        'properties':[{'name':'iotools:scope','value':'Source dependency inventory only; OS, SDK, transitive Maven/Gradle and final binary linkage are not exhaustively inventoried'},
                                      {'name':'iotools:source-sha','value':sha}]},
            'components':sorted(components,key=lambda x:x['bom-ref']),
            'compositions':[{'aggregate':'incomplete'}]}

def build_provenance(proof,receipts,sbom_digest):
    return {'schema':1,'kind':'unsigned-build-record','authenticated':False,
            'warning':'Not signed or authenticated; no SLSA level is claimed. Verify checksums and the linked GitHub Actions run independently.',
            'repository':REPOSITORY,'source_sha':proof['source_sha'],
            'source_inputs_sha256':proof['source_inputs_sha256'],'tag':proof['tag'],
            'invocation':{'run_id':proof['run_id'],'run_attempt':proof['run_attempt'],
                          'url':f"https://github.com/{REPOSITORY}/actions/runs/{proof['run_id']}/attempts/{proof['run_attempt']}"},
            'subjects':[{'name':row['file'],'sha256':row['sha256']} for row in sorted(receipts,key=lambda x:x['file'])],
            'source_sbom_sha256':sbom_digest}
