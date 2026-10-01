"""Package a complete offline Flutter Web UI embedded in its local Go gateway."""
from pathlib import Path
import hashlib,json,os,shutil,subprocess,sys,zipfile
root=Path(__file__).resolve().parents[2]
target,arch=sys.argv[1:3]
sha=os.environ.get('IOTOOLS_SHA','local')
bundle=root/f'platform-dist/web-{target}-{arch}'
binary=bundle/('iotools-web.exe' if target=='windows' else 'iotools-web')
assert binary.is_file() and binary.stat().st_size>1000000
(bundle/'使用说明.txt').write_text('''iotools Flutter Web 本机版

运行同目录的 iotools-web（Windows 为 iotools-web.exe），在浏览器打开终端输出的 http://127.0.0.1:端口 地址。
程序仅监听本机，Flutter 界面、字体、CanvasKit 和 Go 协议内核都随此包提供，无需外部 CDN。
浏览器中的 TCP 等协议由此本机 Go 进程执行；单独把网页上传到静态网站不能提供完整协议功能。
默认数据目录是当前用户配置目录下的 iotools-web；可用 -data 指定一个专用目录。请勿使用文件系统根目录。
Ctrl+C 关闭网关并取消活动操作。页面隐藏会取消本页活动操作，返回页面不会自动重发。
文件导出使用浏览器下载，页面提示下载已发起不等于文件已经保存完成。
本包不包含远程云端服务，不支持直接操作 Android USB。请只连接你获准操作的设备。
macOS 构建未经 Apple Developer ID 公证。如系统不允许启动，请先使用已验证的受支持构建环境；不要绕过安全警告。
这是开发测试构建；各平台的实际构建、协议、界面覆盖请以同次 CI 验收记录为准。
''',encoding='utf-8')
licenses=bundle/'licenses';licenses.mkdir(exist_ok=True)
shutil.copyfile(root/'LICENSE',licenses/'iotools-LICENSE')
subprocess.run(['go','run','./scripts/notices','-goos','darwin' if target=='macos' else target,'-goarch',arch,'-cgo','0',str(licenses/'Go'),'./cmd/iotools-web'],cwd=root,check=True)
# Flutter's generated notices are included in the embedded assets and alongside
# the executable for inspection without launching the browser.
notices=root/'mobile/build/web/assets/NOTICES'
assert notices.is_file()
shutil.copyfile(notices,licenses/'Flutter-NOTICES')
for notice in (root/'mobile/build/web/fonts').glob('*OFL.txt'):
    shutil.copyfile(notice,licenses/notice.name)
archive=root/f'platform-dist/iotools-flutter-web-{target}-{arch}-{sha}.zip'
with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED) as z:
    for file in sorted(bundle.rglob('*')):
        if file.is_file():z.write(file,str(Path(bundle.name)/file.relative_to(bundle)))
report={'source_sha':sha,'platform':target,'arch':arch,'file':archive.name,'bytes':archive.stat().st_size,'sha256':hashlib.sha256(archive.read_bytes()).hexdigest(),'binary_sha256':hashlib.sha256(binary.read_bytes()).hexdigest(),'mode':'offline Flutter Web + same-origin loopback Go gateway'}
(root/f'platform-dist/web-{target}-{arch}-manifest.json').write_text(json.dumps(report,indent=2)+'\n')
print(json.dumps(report))
