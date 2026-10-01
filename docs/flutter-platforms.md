# Flutter 多平台构建与验收

原命令行/TUI 继续保留在 `cmd/iotools`。新 Flutter 界面共用 Go 协议引擎，Android 使用原 JNI/MethodChannel；Windows、Linux、macOS 使用打包的 C ABI 动态库；iOS 使用静态归档与进程符号；Web 使用同源、本机回环 Go 网关。

## 构建

CI 定义在 `.github/workflows/flutter-platforms.yml`，固定 Flutter 3.35.7、Go 版本以 go.mod 为准。不要把单独的 Flutter 壳或静态网页误认为完整产品。

- Linux：先运行 `python3 scripts/flutter-platforms/prepare-native-fonts.py` 准备经过哈希校验的中文/表情字体，再运行 `bash scripts/flutter-platforms/build-native.sh linux amd64`，按输出设置 `IOTOOLS_NATIVE_LIBRARY`，再构建 Flutter Linux。ARM64 要在对应原生架构构建。保留整个 bundle/lib/data，字体随 data 一起提供，需 GTK 3 等系统库
- Windows：随包提供 CJK/emoji 字体（含普通结果、分页内容和 YAML 编辑器），需要官方 Flutter、Visual Studio C++ 桌面工具与 MinGW C 编译器；先构建 Go DLL，再构建 Flutter。CI 会带上程序本地的 Visual C++ 和 Go DLL 所需运行库，以及未改动的微软接收者条款、官方 REDIST 清单和来源记录；Windows/Linux 不打包 Android 传递依赖的可选 JNI/JVM 库
- macOS：随包提供 CJK/emoji 字体，最低 macOS 13。为目标 arm64 或 amd64 架构构建 Go dylib，运行 `bash scripts/flutter-platforms/build-macos.sh arm64`（Intel 用 amd64）；脚本保持应用沙箱，最终验证签名。每个下载包只含其命名的原生架构，不重复打包通用应用。当前为 ad-hoc 测试签名，未公证
- iOS：先为当前 Mac 架构构建 simulator Go archive，再构建模拟器应用；设备版另建 arm64 archive，通过 `prepare-ios-device-release.py` 暂时仅移除 `integration_test` 开发依赖后执行正常 Flutter Release/未签名构建。所有生产依赖及传递依赖的版本/来源/锁定条目、应用/Go/资源字节必须不变，原 pubspec 与 lock 最终恢复；实际设备包禁止测试插件注册、框架、符号和通道，不进行构建后的二进制删除。模拟器包不等于设备 IPA；未签名设备包需要用户自己的 Apple 签名流程，不可直接在 iPhone 安装。设备编译独立运行；模拟器先发布仅编译/静态检查通过的 Debug 测试开发包（含 integration_test 截图/测试结果通道，不作为生产包），不宣称安装、启动或 XCTest 通过；随后普通启动及五项 XCTest 是不可忽略失败的独立必需门禁，只有通过后才另标 verified。各产物 manifest 明确区分范围，均附精确 Go/项目许可证
- Web：`bash scripts/flutter-platforms/build-web.sh linux amd64`（或 windows、macos 与相应架构）把 Flutter、CanvasKit、固定 SHA256 的官方 OFL 字体与 Go 网关打在同一可执行文件中。运行 iotools-web，打开其输出的 `http://127.0.0.1:端口`。不监听局域网，不依赖远端协议服务；单独静态托管网页不支持直接 TCP/串口设备连接

`IOTOOLS_SHA` 将写入产物和 manifest。CI 包含下载包 SHA256 与平台依赖证据。不要单凭编译成功推断所有协议或物理设备已验收。

iOS XCTest 命令单独使用 `ONLY_ACTIVE_ARCH=NO`，同时继续固定 `ARCHS` 为已验证应用的唯一架构。
[Apple 构建设置说明](https://developer.apple.com/documentation/xcode/build-settings-reference)将 `ARCHS` 定义为产物架构列表；关闭 active-only 限制不会加入该列表之外的架构。
这是针对测试目标与宿主设置差异的受控尝试，不代表已经确定模拟器目的地发现失败的原因。
测试前记录原 `YES` 与当前 `NO` 命令、Runner/RunnerTests 设置及 `test -showdestinations` 只读诊断；后者需当前 Xcode 的 `-help` 确认该信息选项。
只执行一次真实 XCTest，保留相同模拟器 UUID、SDK、原有超时和全部五项断言；诊断输出不能代替通过结果。

## 安全边界

- 不采用浏览器 URL 传入的任意网关地址；Web 要求同源、精确 Host/Origin、CSRF、HttpOnly SameSite cookie 和独立会话
- Web 页面隐藏/会话过期、原生窗口隐藏会取消操作，恢复不自动重发；取消写入预览不会发出写入
- 配置、导入和导出都限制在应用私有目录。平台根目录别名先规范化，外部 JSON 不因此获得任意路径访问
- 用户主动导入/导出通过系统选择器或浏览器上传/下载；Web 的“下载已发起”不冒称保存成功
- 不配置 Apple/Windows 发行者凭据，不关闭系统或应用安全保护，不指导绕过安全警告

## 能力与交付边界

每份包按其自己的 source SHA 和平台证据判断。共享测试与 HTTP 冒烟不表示全部 69 项高级路径、现场工业设备或所有操作系统环境均已验收。

- 桌面原生串口需要操作系统已授予相应设备权限；Windows/Linux/macOS 不提供 Android USB 适配器接口，未支持操作会明确报错
- iOS 未开放普通桌面串口/Android USB 能力；未签名设备包不能直接安装到 iPhone，模拟器不能替代真机外设验收
- Web 的 MQTT/Kafka/Modbus/OPC 等网络功能由同源本机 Go 网关执行，静态前端本身不能直接访问原始 TCP 或串口；此包未启用 Web Serial/USB
- macOS 保持 App Sandbox；网络客户/服务端与串口权限声明不等于外设访问或 OAuth 浏览器回调已经现场验证。未公证测试包不能被描述为任意 Mac 均可直接运行
- Android 使用既有独立 JNI/USB 适配器；模拟器协议回归不证明真实 ARM64 手机的 USB 线缆、驱动、权限弹窗和具体设备均通过

## 验收分层

1. Go race、实际 C ABI 和 Dart FFI：UTF-8、UInt64 数字/字符串区分、取消、独立会话、路径与资源释放
2. Flutter 单元/控件与 Chrome 契约：布局、输入、确认、隐藏/恢复、平台能力、文件流程
3. Linux/Windows/macOS 原生窗口：真实 Go HTTP、取消/确认、精确线上字节、隐藏后失效和无重放，截图由实际渲染获取；Linux 字体测试使用进程专用的空 fontconfig，不能借用 CI 系统中文字体，并分别保留中文/emoji 的 YAML 编辑器与 HTTP 正文截图；macOS 测试 app 的实际签名另验 App Sandbox 与网络权限
4. Windows 最终 Release ZIP：另从实际 ZIP 解压启动原始 iotools.exe，在子进程中清除 Java 环境变量并收窄 PATH；通过系统 MSAA/IAccessible（固定 Flutter 3.35 的原生可访问性接口）读取可见控件，再用真实鼠标点击完成写入预览/取消/确认，校验真实 HTTP 字节、截图和无 JVM/JNI 加载的模块列表。它与 Debug Flutter 集成测试分开记录，失败不会上传最终包
5. Web：实际打包网关 HTTP/API/文件/安全验收；Chrome 真实界面驱动确认、精确 HTTP 字节、刷新不重放，并阻止外部资源请求
6. iOS：Mach-O 架构/平台/全部 FFI 符号、模拟器正常启动、实际 Go ABI 与导出边界 XCTest、真实系统选择器呈现；设备包验证未签名

初始候选 b11ba12 的 Linux x64/ARM64 实际编译和引擎/Flutter 测试通过，但其打包大小假设失败；Windows/macOS 在根目录规范化处失败；Chrome Web 契约及编译通过。这些都不代表该候选已完成全部平台交付。后续以各次 workflow 的真实结果为准。

仍需用户环境验收：实际串口/USB/工业设备、签名与物理 iPhone、iOS 全协议交互及选定文件提供商的完整保存。CI 的局部验证不应被描述成这些项目已经完成。
