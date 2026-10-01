#!/usr/bin/env python3
"""Prove the mobile build is Flutter and contains no old terminal/native UI."""
from pathlib import Path
import sys,zipfile
root=Path(__file__).resolve().parents[2]
for p in (root/'mobile').rglob('*'):
 if not p.is_file() or any(x in p.parts for x in ['build','.dart_tool','.gradle']):continue
 if p.suffix not in ('.dart','.java','.xml','.gradle','.yaml'):continue
 text=p.read_text()
 for removed in ('TerminalCanvas','mobiletty','xterm','com.rivo.tview','tcell.Screen','android.webkit.WebView'):
  assert removed not in text, f'{p}: legacy mobile renderer {removed}'
java=list((root/'mobile/android/app/src/main/java/io/github/liveyum/iotools').rglob('*.java'))
allowed={'MainActivity.java','NativeRuntime.java','MobileFiles.java','UsbSerialTransport.java'}
assert {p.name for p in java}==allowed, 'Unexpected legacy Java UI class in Flutter APK source'
assert 'extends FlutterActivity' in next(p for p in java if p.name=='MainActivity.java').read_text()
if len(sys.argv)>1:
 with zipfile.ZipFile(sys.argv[1]) as apk:
  names=set(apk.namelist())
  assert any(x.endswith('/libflutter.so') for x in names),'Flutter engine missing'
  assert any(x.startswith('assets/flutter_assets/') for x in names),'Flutter assets missing'
  abis=[abi for abi in ('arm64-v8a','x86_64') if f'lib/{abi}/libflutter.so' in names]
  assert abis,'No supported Flutter ABI'
  for abi in abis:assert f'lib/{abi}/libiotools.so' in names,f'Go engine missing for {abi}'
  for name in names:
   if name.endswith('.dex'):
    data=apk.read(name)
    for banned in (b'TerminalCanvas',b'Lio/github/liveyum/iotools/NativeUi;',b'Lio/github/liveyum/iotools/AdvancedWorkflows;',b'Lio/github/liveyum/iotools/OpcuaWorkspace;'):
     assert banned not in data,f'Removed UI class in APK: {banned}'
 print('APK contains actual Flutter engine and shared Go engine; legacy mobile UI absent')
print('Flutter source isolation passed')
