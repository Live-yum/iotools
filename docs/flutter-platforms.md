# Flutter 多平台构建与验收

原命令行/TUI 继续保留在 `cmd/iotools`。新 Flutter 界面共用 Go 协议引擎，Android 使用原 JNI/MethodChannel；Windows、Linux、macOS 使用打包的 C ABI 动态库；iOS 使用静态归档与进程符号；Web 使用同源、本机回环 Go 网关。

## 构建

CI 定义在 `.github/workflows/flutter-platforms.yml`，固定 Flutter 3.35.7、Go 版本以 go.mod 为准。不要把单独的 Flutter 壳或静态网页误认为完整产品。

- Linux：先运行 `python3 scripts/flutter-platforms/prepare-native-fonts.py` 准备经过哈希校验的中文/表情字体，再运行 `bash scripts/flutter-platforms/build-native.sh linux amd64`，按输出设置 `IOTOOLS_NATIVE_LIBRARY`，再构建 Flutter Linux。ARM64 要在对应原生架构构建。保留整个 bundle/lib/data，字体随 data 一起提供，需 GTK 3 等系统库
- Windows：需要官方 Flutter、Visual Studio C++ 桌面工具与 MinGW C 编译器；先构建 Go DLL，再构建 Flutter。CI 会带上程序本地的 Visual C++ 和 Go DLL 所需运行库
- macOS：最低 macOS 13。为目标 arm64 或 amd64 架构构建 Go dylib，运行 `bash scripts/flutter-platforms/build-macos.sh arm64`（Intel 用 amd64）；脚本保持应用沙箱，最终验证签名。每个下载包只含其命名的原生架构，不重复打包通用应用。当前为 ad-hoc 测试签名，未公证
- iOS：先为当前 Mac 架构构建 simulator Go archive，再构建模拟器应用；设备版另建 arm64 archive，执行 `flutter build ios --release --no-codesign`。模拟器包不等于设备 IPA；未签名设备包需要用户自己的 Apple 签名流程，不可直接在 iPhone 安装
- Web：`bash scripts/flutter-platforms/build-web.sh linux amd64`（或 windows、macos 与相应架构）把 Flutter、CanvasKit、固定 SHA256 的官方 OFL 字体与 Go 网关打在同一可执行文件中。运行 iotools-web，打开其输出的 `http://127.0.0.1:端口`。不监听局域网，不依赖远端协议服务；单独静态托管网页不支持直接 TCP/串口设备连接

`IOTOOLS_SHA` 将写入产物和 manifest。CI 包含下载包 SHA256 与平台依赖证据。不要单凭编译成功推断所有协议或物理设备已验收。

## 安全边界

- 不采用浏览器 URL 传入的任意网关地址；Web 要求同源、精确 Host/Origin、CSRF、HttpOnly SameSite cookie 和独立会话
- Web 页面隐藏/会话过期、原生窗口隐藏会取消操作，恢复不自动重发；取消写入预览不会发出写入
- 配置、导入和导出都限制在应用私有目录。平台根目录别名先规范化，外部 JSON 不因此获得任意路径访问
- 用户主动导入/导出通过系统选择器或浏览器上传/下载；Web 的“下载已发起”不冒称保存成功
- 不配置 Apple/Windows 发行者凭据，不关闭系统或应用安全保护，不指导绕过安全警告

## 验收分层

1. Go race、实际 C ABI 和 Dart FFI：UTF-8、UInt64 数字/字符串区分、取消、独立会话、路径与资源释放
2. Flutter 单元/控件与 Chrome 契约：布局、输入、确认、隐藏/恢复、平台能力、文件流程
3. Linux/Windows 原生窗口：真实 Go HTTP、取消/确认、精确线上字节、隐藏后失效和无重放，截图由实际渲染获取；Linux 字体测试使用进程专用的空 fontconfig，不能借用 CI 系统中文字体
4. Web：实际打包网关 HTTP/API/文件/安全验收；Chrome 真实界面驱动确认、精确 HTTP 字节、刷新不重放，并阻止外部资源请求
5. iOS：Mach-O 架构/平台/全部 FFI 符号、模拟器正常启动、实际 Go ABI 与导出边界 XCTest、真实系统选择器呈现；设备包验证未签名

初始候选 b11ba12 的 Linux x64/ARM64 实际编译和引擎/Flutter 测试通过，但其打包大小假设失败；Windows/macOS 在根目录规范化处失败；Chrome Web 契约及编译通过。这些都不代表该候选已完成全部平台交付。后续以各次 workflow 的真实结果为准。

仍需用户环境验收：实际串口/USB/工业设备、签名与物理 iPhone、iOS 全协议交互及选定文件提供商的完整保存、macOS 真实窗口交互。CI 的局部验证不应被描述成这些项目已经完成。
