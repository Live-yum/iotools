#!/usr/bin/env python3
"""Hermetic fail-closed tests. No real API calls, signing, uploads or publication."""
import copy
import argparse
import os
import hashlib
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
import urllib.request
import zipfile

import release as r
from android_report import check_notices
from build_tui import check_macos_dependencies

SHA = '1' * 40
IDENTITY = {'source_sha': SHA, 'tag': r.VERSION, 'source_inputs_sha256': 'a' * 64}


class FakeAPI:
    def __init__(self, answers):
        self.answers = answers
        self.calls = []
    def request(self, path, *args, **kwargs):
        self.calls.append(path)
        return copy.deepcopy(self.answers[path])
    def optional(self, path):
        self.calls.append(path)
        return copy.deepcopy(self.answers.get(path))
    def jobs(self, run_id):
        return copy.deepcopy(self.answers['jobs'])


class SourceTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        subprocess.run(['git', 'init', '-q', str(self.root)], check=True)
        r.git('config', 'user.email', 'test@example.invalid', root=self.root)
        r.git('config', 'user.name', 'Test', root=self.root)
        (self.root/'mobile').mkdir()
        (self.root/'mobile/pubspec.yaml').write_text('version: 0.3.1+4\n')
        (self.root/'go.mod').write_text('module fixture\ngo 1.27.1\n')
        self.base = self.commit()
    def tearDown(self):
        self.temp.cleanup()
    def commit(self):
        r.git('add', '.', root=self.root)
        r.git('commit', '-qm', 'fixture', root=self.root)
        return r.git('rev-parse', 'HEAD', root=self.root)
    def test_exact_release_only(self):
        self.assertEqual(r.identity(self.base, root=self.root)['source_sha'], self.base)
        with self.assertRaisesRegex(RuntimeError, 'Only the reviewed'):
            r.identity(self.base, 'v0.4.0', self.root)
    def test_utf8_version_file_is_read_explicitly(self):
        path=self.root/'mobile/pubspec.yaml'
        path.write_text('# 中文应用\nversion: 0.3.1+4\n',encoding='utf-8')
        read=Path.read_text
        def guarded(path,*args,**kwargs):
            if path.name=='pubspec.yaml':self.assertEqual(kwargs.get('encoding'),'utf-8')
            return read(path,*args,**kwargs)
        with patch.object(Path,'read_text',guarded):
            self.assertEqual(r.identity(self.base,root=self.root)['tag'],'v0.3.1')

    def test_wrong_checkout_rejected(self):
        with self.assertRaisesRegex(RuntimeError, 'Checkout'):
            r.identity(SHA, root=self.root)
    def test_version_mismatch_rejected(self):
        (self.root/'mobile/pubspec.yaml').write_text('version: 0.4.0+4\n')
        with self.assertRaisesRegex(RuntimeError, 'version disagree'):
            r.identity(self.base, root=self.root)
    def test_release_or_harness_changes_are_not_equivalence_exceptions(self):
        for name in ('scripts/release/build_android.sh', 'scripts/release/release.py',
                     'scripts/flutter-platforms/verify-ios.py'):
            self.assertTrue(r.is_input(name))
        p = self.root/'scripts/release/release.py';p.parent.mkdir(parents=True);p.write_text('# fixture\n')
        with self.assertRaisesRegex(RuntimeError, 'inputs differ'):
            r.compare_sources(self.commit(), self.base, self.root)
    def test_complete_tree_digest_is_recorded(self):
        proof = r.compare_sources(self.base, self.base, self.root)
        self.assertEqual(proof['candidate_tree'], proof['baseline_tree'])
        self.assertEqual(proof['excluded_differences'], {})
        self.assertEqual(r.EXCLUDED_FILES, set())
    def test_new_unreviewed_workflow_rejected(self):
        p=self.root/'.github/workflows/unreviewed.yml';p.parent.mkdir(parents=True);p.write_text('bad')
        with self.assertRaisesRegex(RuntimeError, 'inputs differ'):
            r.compare_sources(self.commit(), self.base, self.root)
    def test_compiler_config_rejected(self):
        (self.root/'go.mod').write_text('module fixture\ngo 1.28.0\n')
        with self.assertRaisesRegex(RuntimeError, 'go.mod'):
            r.compare_sources(self.commit(), self.base, self.root)
    def test_lockfile_added_rejected(self):
        (self.root/'mobile/pubspec.lock').write_text('unexpected')
        with self.assertRaisesRegex(RuntimeError, 'pubspec.lock'):
            r.compare_sources(self.commit(), self.base, self.root)
    def test_build_script_not_excluded(self):
        for path in ('scripts/flutter-platforms/build-native.sh', 'mobile/ios/Runner.xcodeproj/project.pbxproj',
                     'scripts/flutter/verify-mobile.py', '.github/workflows/android.yml'):
            self.assertTrue(r.is_input(path), path)
    def test_tracked_mutation_fails(self):
        (self.root/'go.mod').write_text('changed')
        with self.assertRaisesRegex(RuntimeError, 'Unreviewed tracked build mutation'):
            r.check_worktree(self.root)
    def test_clean_tree_passes(self):
        self.assertEqual(r.check_worktree(self.root), {})


class HistoryTests(unittest.TestCase):
    def setUp(self):
        self.spec = {**r.HISTORY[0], 'sha': '2' * 40, 'run_id': 1}
    def api(self, spec=None):
        spec = spec or self.spec
        return FakeAPI({f'/actions/runs/{spec["run_id"]}': {'repository': {'full_name': r.REPOSITORY},
                        'head_sha': spec['sha'], 'path': spec['workflow'], 'status': 'completed',
                        'conclusion':'success', 'html_url':'https://github.com/Live-yum/iotools/actions/runs/1', 'run_attempt':1},
                        'jobs': [{'id': index, 'name': name, 'status':'completed', 'conclusion':'success',
                                  'steps':[{'name':step,'conclusion':'success'} for step in
                                           spec['steps'] + spec.get('job_steps', {}).get(name, [])]}
                                 for index,name in enumerate(spec['jobs'])]})
    def test_exact_runtime_success(self):
        result=r.verify_historical(self.api(),self.spec)
        self.assertEqual(result['sha'],self.spec['sha'])
    def test_wrong_runtime_sha_rejected(self):
        a=self.api();a.answers['/actions/runs/1']['head_sha']=SHA
        with self.assertRaisesRegex(RuntimeError,'SHA mismatch'):r.verify_historical(a,self.spec)
    def test_wrong_workflow_rejected(self):
        a=self.api();a.answers['/actions/runs/1']['path']='.github/workflows/wrong.yml'
        with self.assertRaisesRegex(RuntimeError,'workflow path'):r.verify_historical(a,self.spec)
    def test_failed_job_rejected(self):
        a=self.api();a.answers['jobs'][0]['conclusion']='failure'
        with self.assertRaisesRegex(RuntimeError,'job not successful'):r.verify_historical(a,self.spec)
    def test_skipped_required_gate_rejected(self):
        a=self.api();a.answers['jobs'][0]['steps'][0]['conclusion']='skipped'
        with self.assertRaisesRegex(RuntimeError,'gate missing/failed'):r.verify_historical(a,self.spec)
    def test_full_platform_run_must_succeed_including_ios(self):
        spec={**r.HISTORY[-1], 'sha': SHA, 'run_id': 1};a=self.api(spec)
        self.assertEqual(len(spec['jobs']), 8)
        self.assertIn('ios', spec['jobs'])
        a.answers['/actions/runs/1']['conclusion']='failure'
        with self.assertRaisesRegex(RuntimeError,'not successful'):r.verify_historical(a,spec)
    def test_required_ios_and_windows_icon_steps_cannot_skip(self):
        for template in r.HISTORY[2:]:
            spec={**template, 'sha': SHA, 'run_id': 1}
            for name, required in spec.get('job_steps', {}).items():
                a=self.api(spec)
                job=next(j for j in a.answers['jobs'] if j['name']==name)
                next(s for s in job['steps'] if s['name']==required[0])['conclusion']='skipped'
                with self.assertRaisesRegex(RuntimeError,'gate missing/failed'):r.verify_historical(a,spec)
    def test_latest_failed_run_not_hidden_by_older_green(self):
        spec=self.spec;api=self.api()
        api.answers['/actions/runs?head_sha='+spec['sha']+'&per_page=100']={'workflow_runs':[
            {'id':1,'head_sha':spec['sha'],'path':spec['workflow']},
            {'id':2,'head_sha':spec['sha'],'path':spec['workflow']}]}
        api.answers['/actions/runs/2']={**api.answers['/actions/runs/1'],'conclusion':'failure'}
        with self.assertRaisesRegex(RuntimeError,'not successful'):r.acceptance_for_source(api,spec['sha'])
    def test_missing_workflow_is_not_success(self):
        api=FakeAPI({'/actions/runs?head_sha='+SHA+'&per_page=100':{'workflow_runs':[]}})
        with self.assertRaises(r.MissingAcceptance):r.acceptance_for_source(api,SHA)
    def test_failed_current_run_never_falls_back_to_parent(self):
        with patch.object(r,'git',return_value=SHA+' '+'2'*40), patch.object(r,'compare_sources',return_value={}), \
             patch.object(r,'acceptance_for_source',side_effect=RuntimeError('pending or failed')) as check:
            with self.assertRaisesRegex(RuntimeError,'pending or failed'):r.verify_acceptance(FakeAPI({}),SHA)
            self.assertEqual(check.call_count,1)
    def test_missing_current_suite_can_use_identical_direct_parent(self):
        with patch.object(r,'git',return_value=SHA+' '+'2'*40), patch.object(r,'compare_sources',return_value={'same':True}), \
             patch.object(r,'acceptance_for_source',side_effect=[r.MissingAcceptance('none'), ['accepted']]):
            self.assertEqual(r.verify_acceptance(FakeAPI({}),SHA), (['accepted'],{'same':True}))
    def test_changed_parent_application_inputs_cannot_qualify(self):
        with patch.object(r,'git',return_value=SHA+' '+'2'*40), \
             patch.object(r,'compare_sources',side_effect=[{},RuntimeError('application inputs changed')]), \
             patch.object(r,'acceptance_for_source',side_effect=r.MissingAcceptance('none')) as check:
            with self.assertRaisesRegex(RuntimeError,'application inputs changed'):r.verify_acceptance(FakeAPI({}),SHA)
            self.assertEqual(check.call_count,1)


class PublishRevalidationTests(unittest.TestCase):
    def test_parent_acceptance_drift_after_upload_never_publicizes_draft(self):
        with tempfile.TemporaryDirectory() as directory:
            folder = Path(directory)
            proof = {**IDENTITY, 'publish_requested': True, 'run_id': '7', 'run_attempt': '1',
                     'historical': [{'sha': '2' * 40}], 'source_equivalence': [{'same': True}]}
            for key in r.EXPECTED:
                (folder / r.filename(key)).write_bytes(('package-' + key).encode())
            r.write_json(folder / 'manifest.json', proof)
            (folder / 'RELEASE_NOTES.zh-CN.md').write_text('fixture notes', encoding='utf-8')
            (folder / 'SHA256SUMS').write_text(''.join(
                f'{r.digest(path)}  {path.name}\n' for path in sorted(folder.iterdir())))
            class API:
                def __init__(self):
                    self.tag = None
                    self.calls = []
                    self.uploads = []
                    self.files = {}
                def optional(self, path):
                    return None if self.tag is None else {'object': {'type': 'commit', 'sha': self.tag}}
                def request(self, path, method='GET', data=None, raw=False):
                    self.calls.append((method, path))
                    if path == '/git/refs':
                        self.tag = data['sha']; return {}
                    if path == '/releases':
                        return {'id': 1, 'draft': True, 'upload_url': 'https://uploads.github.com/repos/Live-yum/iotools/releases/1/assets{?name,label}'}
                    if method == 'POST' and path.startswith('https://uploads.'):
                        idx = len(self.uploads) + 1
                        self.files[idx] = data.read_bytes()
                        self.uploads.append({'id': idx, 'name': data.name, 'state': 'uploaded', 'size': data.stat().st_size})
                        return {}
                    if path == '/releases/1/assets?per_page=100': return self.uploads
                    if path.startswith('/releases/assets/'): return self.files[int(path.rsplit('/',1)[1])]
                    raise AssertionError('Unexpected API action: '+method+' '+path)
            api = API()
            with patch.object(r, 'GitHub', return_value=api), patch.object(r, 'identity', return_value=IDENTITY), \
                 patch.object(r, 'publication_context'), patch.object(r, 'verify_final_ci'), \
                 patch.object(r, 'verify_acceptance', side_effect=[
                    (proof['historical'], proof['source_equivalence'][0]),
                    ([{'sha': '2' * 40, 'run_attempt': 2}], proof['source_equivalence'][0])]), \
                 patch.dict(os.environ, {'GITHUB_RUN_ID':'7','GITHUB_RUN_ATTEMPT':'1','GITHUB_EVENT_NAME':'workflow_dispatch','GITHUB_REF':'refs/heads/main'}):
                with self.assertRaisesRegex(RuntimeError, 'acceptance changed'):
                    r.publish(argparse.Namespace(folder=str(folder), sha=SHA))
            self.assertEqual(len(api.uploads), 22)
            self.assertIn(('POST','/releases'), api.calls)
            self.assertFalse(any(method == 'PATCH' for method, _ in api.calls))


class PublicationTests(unittest.TestCase):
    def context_api(self):
        return FakeAPI({'/git/ref/heads/main':{'object':{'sha':SHA}},
                        '/compare/'+SHA+'...'+SHA:{'status':'identical'}})
    def test_branch_push_requires_explicit_dry_dispatch(self):
        with self.assertRaisesRegex(RuntimeError,'dry-run dispatch'):
            r.publication_context(FakeAPI({}),SHA,'push','refs/heads/feat/unified-portable-tui',False)
    def test_dry_dispatch_has_no_tag_side_effect(self):
        api=FakeAPI({});r.publication_context(api,SHA,'workflow_dispatch','refs/heads/feature',False)
        self.assertEqual(api.calls,[])
    def test_branch_push_cannot_publish(self):
        with self.assertRaisesRegex(RuntimeError,'dry-run dispatch'):
            r.publication_context(FakeAPI({}),SHA,'push','refs/heads/feat/unified-portable-tui',True)
    def test_manual_feature_publication_rejected(self):
        with self.assertRaisesRegex(RuntimeError,'main'):
            r.publication_context(FakeAPI({}),SHA,'workflow_dispatch','refs/heads/feature',True)
    def test_main_publication_gate(self):
        r.publication_context(self.context_api(),SHA,'workflow_dispatch','refs/heads/main',True)
    def test_tag_conflict_rejected(self):
        a=self.context_api();a.answers['/git/ref/tags/'+r.VERSION]={'object':{'type':'commit','sha':'2'*40}}
        with self.assertRaisesRegex(RuntimeError,'different SHA'):
            r.publication_context(a,SHA,'workflow_dispatch','refs/heads/main',True)
    def test_unmerged_source_rejected(self):
        a=self.context_api();a.answers['/compare/'+SHA+'...'+SHA]={'status':'diverged'}
        with self.assertRaisesRegex(RuntimeError,'not merged'):
            r.publication_context(a,SHA,'workflow_dispatch','refs/heads/main',True)
    def test_existing_release_rejected(self):
        a=self.context_api();a.answers['/releases/tags/'+r.VERSION]={'draft':True}
        with self.assertRaisesRegex(RuntimeError,'already exists'):
            r.publication_context(a,SHA,'workflow_dispatch','refs/heads/main',True)
    def test_annotated_tag_is_peeled(self):
        api=FakeAPI({'/git/ref/tags/'+r.VERSION:{'object':{'type':'tag','sha':'2'*40}},
                     '/git/tags/'+'2'*40:{'object':{'type':'commit','sha':SHA}}})
        self.assertEqual(r.resolve_tag(api,r.VERSION),SHA)
    def test_cross_host_redirect_strips_token(self):
        req=urllib.request.Request('https://api.github.com/repos/Live-yum/iotools/releases/assets/1',headers={'Authorization':'Bearer secret'})
        new=r.SafeRedirect().redirect_request(req,None,302,'Found',{},'https://release-assets.githubusercontent.com/file')
        self.assertIsNone(new.get_header('Authorization'))
    def test_insecure_redirect_rejected(self):
        req=urllib.request.Request('https://api.github.com/repos/Live-yum/iotools/releases/assets/1')
        with self.assertRaisesRegex(RuntimeError,'Insecure'):
            r.SafeRedirect().redirect_request(req,None,302,'Found',{},'http://example.invalid/file')
    def test_final_ci_missing_rejected(self):
        api=FakeAPI({'/actions/workflows/ci.yml/runs?head_sha='+SHA+'&per_page=100':{'workflow_runs':[]}})
        with self.assertRaisesRegex(RuntimeError,'No ordinary'):r.verify_final_ci(api,SHA)
    def test_final_ci_failure_rejected(self):
        api=FakeAPI({'/actions/workflows/ci.yml/runs?head_sha='+SHA+'&per_page=100':{'workflow_runs':[
                    {'id':1,'head_sha':SHA,'head_branch':'main','event':'push','status':'completed','conclusion':'failure'}]}})
        with self.assertRaisesRegex(RuntimeError,'pending or failed'):r.verify_final_ci(api,SHA)

    def test_other_pending_check_blocks_publish(self):
        api=FakeAPI({'/actions/workflows/ci.yml/runs?head_sha='+SHA+'&per_page=100':{'workflow_runs':[
            {'id':1,'head_sha':SHA,'head_branch':'main','event':'push','status':'completed','conclusion':'success','html_url':'https://example.invalid/run'}]},
            '/commits/'+SHA+'/check-runs?per_page=100':{'check_runs':[
                {'id':10,'name':'publish','status':'in_progress','conclusion':None},
                {'id':11,'name':'external-required','status':'queued','conclusion':None}]},
            'jobs':[{'name':'publish','status':'in_progress','check_run_url':'https://api.github.com/repos/Live-yum/iotools/check-runs/10'}]})
        with patch.dict('os.environ',{'GITHUB_RUN_ID':'100'}):
            with self.assertRaisesRegex(RuntimeError,'still pending'):r.verify_final_ci(api,SHA)
    def test_only_own_active_publisher_allowed(self):
        api=FakeAPI({'/actions/workflows/ci.yml/runs?head_sha='+SHA+'&per_page=100':{'workflow_runs':[
            {'id':1,'head_sha':SHA,'head_branch':'main','event':'push','status':'completed','conclusion':'success','html_url':'https://example.invalid/run'}]},
            '/commits/'+SHA+'/check-runs?per_page=100':{'check_runs':[
                {'id':10,'name':'publish','status':'in_progress','conclusion':None}]},
            'jobs':[{'name':'publish','status':'in_progress','check_run_url':'https://api.github.com/repos/Live-yum/iotools/check-runs/10'}]})
        with patch.dict('os.environ',{'GITHUB_RUN_ID':'100'}):
            self.assertEqual(r.verify_final_ci(api,SHA)['head_sha'],SHA)


class PbxTests(unittest.TestCase):
    def test_new_ids_and_object_order_do_not_change_semantics(self):
        from pbx_gate import normalized_digest
        old='{objects={AAAAAAAAAAAAAAAAAAAAAAAA={isa=PBXGroup;children=();};};rootObject=AAAAAAAAAAAAAAAAAAAAAAAA;}'
        generated='{objects={AAAAAAAAAAAAAAAAAAAAAAAA={isa=PBXGroup;children=(BBBBBBBBBBBBBBBBBBBBBBBB,);};BBBBBBBBBBBBBBBBBBBBBBBB={isa=PBXFileReference;path="Pods.xcodeproj";};};rootObject=AAAAAAAAAAAAAAAAAAAAAAAA;}'
        renamed=generated.replace('BBBBBBBBBBBBBBBBBBBBBBBB','CCCCCCCCCCCCCCCCCCCCCCCC')
        self.assertEqual(normalized_digest(old,generated),normalized_digest(old,renamed))
        for changed in [generated.replace('Pods.xcodeproj','evil.xcodeproj'),generated.replace('isa=PBXGroup','isa=PBXFileReference'),generated.replace('children=(', 'other=(')]:
            self.assertNotEqual(normalized_digest(old,generated),normalized_digest(old,changed))

    def test_original_ids_cannot_disappear_and_new_reference_cycles_rejected(self):
        from pbx_gate import normalized_digest
        old='{objects={AAAAAAAAAAAAAAAAAAAAAAAA={isa=PBXGroup;};};}'
        for bad in ['{objects={};}', '{objects={AAAAAAAAAAAAAAAAAAAAAAAA={isa=PBXGroup;};BBBBBBBBBBBBBBBBBBBBBBBB={isa=PBXGroup;children=(BBBBBBBBBBBBBBBBBBBBBBBB,);};};}']:
            with self.assertRaises(ValueError):normalized_digest(old,bad)

    def test_duplicate_keys_and_trailing_tokens_rejected(self):
        from pbx_gate import parse
        for bad in ['{a=1;a=2;}', '{a=1;} unexpected']:
            with self.assertRaises(ValueError):parse(bad)

    def test_quoted_formatting_normalizes_but_scripts_and_list_order_remain(self):
        from pbx_gate import normalized_digest
        old='{objects={AAAAAAAAAAAAAAAAAAAAAAAA={isa=PBXGroup;};};}'
        one='{objects={AAAAAAAAAAAAAAAAAAAAAAAA={isa=PBXGroup;children=(a,b,);shellScript="echo safe\\n";};};}'
        two=one.replace('isa=PBXGroup','isa="PBXGroup"')
        self.assertEqual(normalized_digest(old,one),normalized_digest(old,two))
        self.assertNotEqual(normalized_digest(old,one),normalized_digest(old,one.replace('a,b','b,a')))
        self.assertNotEqual(normalized_digest(old,one),normalized_digest(old,one.replace('echo safe','echo unsafe')))


class PackageTests(unittest.TestCase):
    def test_macos_system_deps_and_arch(self):
        result=check_macos_dependencies('arm64\n','iotools:\n\t/usr/lib/libSystem.B.dylib (compatibility version 1.0.0)\n','arm64')
        self.assertEqual(result,['/usr/lib/libSystem.B.dylib'])
    def test_macos_extra_arch_rejected(self):
        with self.assertRaisesRegex(RuntimeError,'one Mach-O'):
            check_macos_dependencies('x86_64 arm64','x\n /usr/lib/libSystem.B.dylib (x)','arm64')
    def test_macos_non_system_dep_rejected(self):
        with self.assertRaisesRegex(RuntimeError,'non-system'):
            check_macos_dependencies('arm64','x\n @rpath/libcustom.dylib (x)','arm64')
    def test_apksigner_legacy_and_scheme_labels(self):
        from android_report import signing_fingerprint
        prefix='Verifies\nNumber of signers: 1\nV2 Signer: certificate DN: C=US, O=Android, CN=Android Debug\n'
        for label in ['Signer #1','V2 Signer:','V3.1 Signer:']:
            self.assertEqual(signing_fingerprint(prefix+label+' certificate SHA-256 digest: '+SHA+'a'*24+'\n'),SHA+'a'*24)
        with self.assertRaisesRegex(RuntimeError,'exactly one'):
            signing_fingerprint(prefix.replace('signers: 1','signers: 2'))
        with self.assertRaisesRegex(RuntimeError,'inconsistent'):
            signing_fingerprint(prefix+'V2 Signer: certificate SHA-256 digest: '+'a'*64+'\nV3 Signer: certificate SHA-256 digest: '+'b'*64+'\n')
        with self.assertRaisesRegex(RuntimeError,'Missing'):
            signing_fingerprint(prefix+'V2 Signer: public key SHA-256 digest: '+'a'*64+'\n')

    def test_android_missing_licenses_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'test.apk'
            with zipfile.ZipFile(p,'w') as z:z.writestr('assets/LICENSE','test')
            with zipfile.ZipFile(p) as z:
                with self.assertRaisesRegex(RuntimeError,'Missing embedded'):check_notices(z)
    def test_wrong_apk_scope_rejected(self):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'a';p.write_bytes(b'fixture')
            record={'source_sha':SHA,'bytes':p.stat().st_size,'sha256':r.digest(p),'normal_entry_aot':False}
            with self.assertRaisesRegex(RuntimeError,'Normal-entry'):
                r.validate_build_report('android-arm64-v8a-aot-test-signed',record,p,SHA)
    def test_simulator_cannot_claim_runtime_verified(self):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'a';p.write_bytes(b'fixture')
            record={'source_sha':SHA,'bytes':p.stat().st_size,'sha256':r.digest(p),'kind':'simulator','status':'passed_for_stated_scope'}
            with self.assertRaisesRegex(RuntimeError,'compiled-only'):
                r.validate_build_report('ios-simulator-arm64-debug-developer',record,p,SHA)
    def test_trimpath_buildinfo_uses_vcs_revision_not_unrecorded_ldflags(self):
        info = "binary: go1.27.1\n\tbuild\t-trimpath=true\n\tbuild\tGOOS=linux\n\tbuild\tGOARCH=amd64\n\tbuild\tvcs.revision=" + SHA + "\n"
        with tempfile.TemporaryDirectory() as folder:
            asset=Path(folder)/"tui.zip"
            with zipfile.ZipFile(asset,"w") as archive:
                archive.writestr("bundle/iotools",b"ELF"+SHA.encode())
            with patch.object(r.subprocess,"check_output",return_value=info):
                self.assertEqual(r.embedded_revision(asset,"tui-linux-amd64",SHA)[0]["embedded_source_sha"],SHA)
            for bad in [info.replace(SHA,"b"*40),info.replace("GOOS=linux","GOOS=windows"),info.replace("GOARCH=amd64","GOARCH=arm64")]:
                with patch.object(r.subprocess,"check_output",return_value=bad),self.assertRaisesRegex(RuntimeError,"identity/target"):
                    r.embedded_revision(asset,"tui-linux-amd64",SHA)

    def test_embedded_go_sha_required(self):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'test.zip'
            with zipfile.ZipFile(p,'w') as z:z.writestr('bundle/iotools',b'wrong revision')
            with self.assertRaisesRegex(RuntimeError,'missing exact source'):
                r.embedded_revision(p,'tui-linux-amd64',SHA)
    def test_ios_stamp_has_honest_scope(self):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'test.zip'
            with zipfile.ZipFile(p,'w') as z:z.writestr('Runner.app/Runner',SHA.encode())
            report=r.embedded_revision(p,'ios-device-arm64-unsigned',SHA)
            self.assertIn('no standalone',report[0]['build_info_scope'])


class AggregateTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory();self.root=Path(self.temp.name);self.incoming=self.root/'incoming'
        self.proof={**IDENTITY,'run_id':'100','run_attempt':'1','historical':[]}
        self.identity=patch.object(r,'identity',return_value=IDENTITY);self.identity.start()
        self.embedded=patch.object(r,'embedded_revision',return_value=[{'embedded_source_sha':SHA}]);self.embedded.start()
        for key in r.EXPECTED:
            folder=self.incoming/key;folder.mkdir(parents=True)
            file=folder/r.filename(key);file.write_bytes(key.encode())
            report={'source_sha':SHA,'sha256':r.digest(file),'bytes':file.stat().st_size}
            if key.startswith('tui'):
                report.update(platform=key[4:],standalone_runtime_verified=True,smoke_passed=True)
            elif key.startswith('flutter'):
                report.update(platform=key.split('-')[1],runner_arch=key.split('-')[2],native_core_sha256='a'*64)
            elif key.startswith('web'):
                report.update(platform=key.split('-')[1],arch=key.split('-')[2],binary_sha256='a'*64)
            elif key.startswith('android'):
                report.update(abis=['arm64-v8a'] if 'arm64-v8a' in key else ['arm64-v8a','x86_64'],normal_entry_aot=True,test_code_absent=True,
                              signing='debug/test certificate; not production publisher identity')
            elif key.startswith('ios-device'):
                report.update(kind='device',status='passed_for_stated_scope',architectures=['arm64'],signature='unsigned; test',production_isolation={'ok':True},dependency_isolation={'ok':True})
            else:
                report.update(kind='simulator-compiled',status='compiled_only_no_runtime_acceptance',architectures=['arm64'])
            record={**IDENTITY,'key':key,'file':file.name,'sha256':r.digest(file),'bytes':file.stat().st_size,
                    'run_id':'100','run_attempt':'1','build_report':report,'embedded_binaries':[{'embedded_source_sha':SHA}]}
            r.write_json(folder/'receipt.json',record)
    def tearDown(self):
        self.identity.stop();self.embedded.stop();self.temp.cleanup()
    def assemble(self):return r.assemble(self.incoming,self.root/'out',self.proof,SHA)
    def first(self):return next(self.incoming.rglob('receipt.json'))
    def change(self,field,value):
        p=self.first();d=json.loads(p.read_text());d[field]=value;r.write_json(p,d)
    def test_complete_19_assets_and_three_metadata(self):
        result=self.assemble();self.assertEqual(len(result['assets']),19)
        self.assertEqual(len(list((self.root/'out').iterdir())),22)
    def test_missing_asset_rejected(self):
        self.first().unlink()
        with self.assertRaisesRegex(RuntimeError,'Missing release assets'):self.assemble()
    def test_tampered_asset_rejected(self):
        p=self.first();d=json.loads(p.read_text());(p.parent/d['file']).write_text('tampered')
        with self.assertRaisesRegex(RuntimeError,'checksum mismatch'):self.assemble()
    def test_wrong_sha_rejected(self):
        self.change('source_sha','2'*40)
        with self.assertRaisesRegex(RuntimeError,'identity differs'):self.assemble()
    def test_wrong_run_rejected(self):
        self.change('run_id','999')
        with self.assertRaisesRegex(RuntimeError,'different workflow'):self.assemble()
    def test_wrong_attempt_rejected(self):
        self.change('run_attempt','2')
        with self.assertRaisesRegex(RuntimeError,'different workflow'):self.assemble()
    def test_path_traversal_rejected(self):
        self.change('file','../untrusted.apk')
        with self.assertRaisesRegex(RuntimeError,'unsafe asset filename'):self.assemble()
    def test_unexpected_file_rejected(self):
        (self.first().parent/'instrumentation.apk').write_text('test-only')
        with self.assertRaisesRegex(RuntimeError,'Unexpected files'):self.assemble()
    def test_duplicate_key_rejected(self):
        import shutil
        shutil.copytree(self.first().parent,self.incoming/'duplicate')
        with self.assertRaisesRegex(RuntimeError,'duplicate asset key'):self.assemble()
    def test_unsafe_symlink_rejected(self):
        p=self.first();d=json.loads(p.read_text());f=p.parent/d['file'];other=self.root/'external';f.rename(other);f.symlink_to(other)
        with self.assertRaisesRegex(RuntimeError,'unsafe asset'):self.assemble()


if __name__=='__main__':
    unittest.main()
