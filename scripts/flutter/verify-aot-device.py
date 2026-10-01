#!/usr/bin/env python3
"""Turn exact-APK device evidence into a machine-checked acceptance report."""
import hashlib,json,os,pathlib,re,sys,xml.etree.ElementTree as ET
apk=pathlib.Path(sys.argv[1]);evidence=pathlib.Path(sys.argv[2]);log=(evidence/'aot-instrumentation.txt').read_text()
expected=hashlib.sha256(apk.read_bytes()).hexdigest()
assert re.search(r'OK \(1 test\)',log), 'Normal-entry AOT instrumentation did not report one passing test'
assert 'FAILURES!!!' not in log and 'INSTRUMENTATION_FAILED' not in log, 'AOT instrumentation failed'
report_path=evidence/'aot-device'/'aot-verification.json'
report=json.loads(report_path.read_text())
assert report['status']=='passed' and report['restored_private_config_and_preferences'] is True,'Device acceptance or cleanup incomplete'
assert report['build_sha']==os.environ['IOTOOLS_SHA'],'Device build identity mismatch'
assert report['apk_sha256']==expected,'Device APK differs from delivery bytes'
assert report['debuggable'] is False,'Delivery APK must not be a debug-runtime build'
assert all(report['protocols'].get(p) is True for p in ('http','mqtt','kafka','modbus','opcua')),'Missing actual protocol UI checks'
assert all(report.get(key) is True for key in ('startup_recovery_cancel_preserved_original','startup_recovery_opened_valid_collection','startup_recovery_damaged_file_unchanged')), 'Cold-start recovery was not verified through actual controls'
assert report['http_post_cancelled_without_write'] is True and report['http_post_confirmed_once'] is True
suite=ET.Element('testsuite',name='normal-entry-aot-exact-apk',tests='1',failures='0',errors='0',skipped='0')
case=ET.SubElement(suite,'testcase',name='exact_delivery_apk_installed_and_all_five_protocols_operated',classname='AotAcceptanceTest')
ET.SubElement(case,'system-out').text=json.dumps(report,ensure_ascii=False)
ET.ElementTree(suite).write(evidence/'TEST-normal-entry-aot.xml',encoding='utf-8',xml_declaration=True)
print(f'Exact delivery APK verified on device: {expected}')
