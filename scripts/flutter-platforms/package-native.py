"""Package actual native app files; never substitute a placeholder executable."""
from pathlib import Path
import hashlib,json,os,subprocess,sys,zipfile
root=Path(__file__).resolve().parents[2]
target,arch=sys.argv[1:3]
sha=os.environ['IOTOOLS_SHA']
if target=='linux':
    cpu={'amd64':'x64','arm64':'arm64'}[arch]
    bundle=root/f'mobile/build/linux/{cpu}/release/bundle';binary=bundle/'iotools';core=bundle/'lib/libiotools_native.so'
elif target=='windows':
    bundle=root/'mobile/build/windows/x64/runner/Release';binary=bundle/'iotools.exe';core=bundle/'iotools_native.dll'
else:
    bundle=root/'mobile/build/macos/Build/Products/Release/iotools.app';binary=bundle/'Contents/MacOS/iotools';core=bundle/'Contents/Frameworks/libiotools_native.dylib'
for path in (binary,core):
    assert path.is_file() and path.stat().st_size>100000,path
out=root/'platform-dist';out.mkdir(exist_ok=True)
evidence=root/'platform-evidence';evidence.mkdir(exist_ok=True)
if target=='linux':
    for file in (binary,core):
        result=subprocess.run(['ldd',str(file)],check=True,capture_output=True,text=True)
        assert 'not found' not in result.stdout,result.stdout
        (evidence/(file.name+'-ldd.txt')).write_text(result.stdout)
if target=='windows':
    for name in ('msvcp140.dll','vcruntime140.dll','vcruntime140_1.dll'):
        assert (bundle/name).is_file(),name
    result=subprocess.run(['objdump','-p',str(core)],check=True,capture_output=True,text=True)
    (evidence/'native-dll-imports.txt').write_text(result.stdout)
    imports=[line.split('DLL Name:',1)[1].strip() for line in result.stdout.splitlines() if 'DLL Name:' in line]
    system={'kernel32.dll','msvcrt.dll','ucrtbase.dll','ws2_32.dll','ntdll.dll','advapi32.dll','bcrypt.dll'}
    for name in imports:
        if name.lower() in system or name.lower().startswith('api-ms-win-'):continue
        import shutil
        source=Path('C:/msys64/mingw64/bin')/name
        assert source.is_file(),f'Unbundled native dependency {name}'
        shutil.copyfile(source,bundle/name)
license_dir=bundle/('Contents/Resources/licenses' if target=='macos' else 'licenses')
license_dir.mkdir(parents=True,exist_ok=True)
import shutil
shutil.copyfile(root/'LICENSE',license_dir/'iotools-LICENSE')
go_target='darwin' if target=='macos' else target
go_arch='arm64,amd64' if target=='macos' else arch
subprocess.run(['go','run','./scripts/notices','-goos',go_target,'-goarch',go_arch,'-cgo','1',str(license_dir/'Go'),'./cmd/iotools-native'],cwd=root,check=True)
if target=='macos':
    # Added license resources are covered by the final ad-hoc test signature.
    subprocess.run(['codesign','--force','--sign','-','--timestamp=none','--entitlements',str(root/'mobile/macos/Runner/Release.entitlements'),str(bundle)],check=True)
    subprocess.run(['codesign','--verify','--deep','--strict',str(bundle)],check=True)
archive=out/f'iotools-flutter-{target}-{arch}-{sha}.zip'
if target=='macos':
    subprocess.run(['ditto','-c','-k','--sequesterRsrc','--keepParent',str(bundle),str(archive)],check=True)
else:
    with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED,compresslevel=6) as z:
        for file in sorted(bundle.rglob('*')):
            if file.is_file():
                relative=file.relative_to(bundle)
                z.write(file,str(Path(bundle.name)/relative))
report={'source_sha':sha,'platform':target,'runner_arch':arch,'file':archive.name,'bytes':archive.stat().st_size,'sha256':hashlib.sha256(archive.read_bytes()).hexdigest(),'native_core_sha256':hashlib.sha256(core.read_bytes()).hexdigest(),'scope':'Native Flutter compilation and real C ABI tests; UI workflow acceptance is a separate gate'}
(out/'manifest.json').write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps(report))
