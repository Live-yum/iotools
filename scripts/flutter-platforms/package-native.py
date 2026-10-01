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
assert binary.is_file() and binary.stat().st_size > 1024, binary
assert core.is_file() and core.stat().st_size > 100000, core
if target == "linux":
    assert binary.read_bytes()[:4] == b"\x7fELF" and core.read_bytes()[:4] == b"\x7fELF"
    assert (bundle/"lib/libflutter_linux_gtk.so").is_file()
    assert (bundle/"lib/libapp.so").is_file()
font_dir=bundle/('Contents/Resources/iotools-fonts' if target=='macos' else 'data/iotools-fonts')
for name in ('NotoSansSC.ttf','NotoEmoji.ttf'):
    assert (font_dir/name).is_file(), name
if target == "windows":
    assert binary.read_bytes()[:2] == b"MZ" and core.read_bytes()[:2] == b"MZ"
out=root/'platform-dist';out.mkdir(exist_ok=True)
evidence=root/'platform-evidence';evidence.mkdir(exist_ok=True)
if target=='linux':
    for file in (binary,core):
        result=subprocess.run(['ldd',str(file)],check=True,capture_output=True,text=True)
        assert 'not found' not in result.stdout,result.stdout
        (evidence/(file.name+'-ldd.txt')).write_text(result.stdout)
if target=='macos':
    expected={'arm64':'arm64','amd64':'x86_64'}[arch]
    apple_binaries=[binary,core,bundle/'Contents/Frameworks/FlutterMacOS.framework/FlutterMacOS',bundle/'Contents/Frameworks/App.framework/App']
    for file in apple_binaries:
        actual=subprocess.check_output(['xcrun','lipo','-archs',str(file)],text=True).split()
        assert actual==[expected],f'{file}: expected one {expected} architecture, got {actual}'
    (evidence/'macho-architectures.json').write_text(json.dumps({'expected':expected,'verified':[str(file.relative_to(bundle)) for file in apple_binaries]},indent=2)+'\n')
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
instructions={
 'linux': '解压完整 bundle 目录，保留 lib 和 data 子目录；运行 ./iotools。需要带图形会话的 Linux、GTK 3 及其系统依赖。界面中文/表情字体随 data/iotools-fonts 提供。本包基于 Ubuntu 22.04 构建，不是无系统依赖的单文件程序。',
 'windows': '解压完整 Release 目录后运行 iotools.exe。保留同目录 DLL 和 data 子目录；随包包含程序所需 Visual C++ 运行库及 CJK/emoji 字体。此开发构建没有发行者 Authenticode 签名。',
 'macos': '解压并使用完整 iotools.app。最低 macOS 13；保留应用包内资源和随附 CJK/emoji 字体。本包保持 App Sandbox，使用开发测试 ad-hoc 签名，未经 Apple Developer ID 公证。系统如阻止运行，请使用自己的受信签名构建流程；不要绕过安全警告。',
}[target]
readme=bundle/('Contents/Resources/使用说明.txt' if target=='macos' else '使用说明.txt')
readme.write_text('iotools Flutter 原生界面\n\n'+instructions+'\n\nWindows、Linux、macOS 原生界面通过随包 Go 内核执行协议，不是网页套壳。数据保存在当前用户应用数据目录；首次使用请通过应用导入 YAML/ZIP。写操作需在界面预览并确认，取消或隐藏窗口不会自动重发。Android USB 接口不适用于这些桌面平台，桌面串口需要操作系统已授予相应设备权限。\n\n这是开发测试构建，具体已验证范围请参阅同次 CI 证据。原命令行/TUI 版本继续保留在仓库 cmd/iotools，未被此界面取代。\n',encoding='utf-8')
license_dir=bundle/('Contents/Resources/licenses' if target=='macos' else 'licenses')
license_dir.mkdir(parents=True,exist_ok=True)
import shutil
for notice in font_dir.glob('*.txt'):
    shutil.copyfile(notice,license_dir/notice.name)
shutil.copyfile(root/'LICENSE',license_dir/'iotools-LICENSE')
go_target='darwin' if target=='macos' else target
go_arch=arch
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
