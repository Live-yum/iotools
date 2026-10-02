# Flutter 移动端

移动端界面使用 Flutter / Dart，HTTP、Kafka、MQTT、Modbus 和 OPC UA 继续调用同一个 Go 协议内核。Windows 和 Linux 继续使用原来的轻量 TUI。

## 分层

- `mobile/lib/`：请求、历史、设置，以及五种协议的触屏工作区。界面与业务交互在 Flutter 中实现
- `mobile/android/`：FlutterActivity 宿主、系统文件选择器、USB Host 和结构化 JNI 适配。这里没有终端、网页壳或旧 Java 业务界面
- `internal/mobileapi/`：有界 JSON 命令、预览确认、取消、配置与历史操作
- `internal/engine/`：桌面端与移动端共用的真实协议实现

Dart 与 Android 之间传递 JSON 字符串，避免平台数值转换损失 Int64/UInt64 精度。请求写入、链式依赖写入、证书例外和危险管理动作分别进行明确的目标预览与确认。切到后台会取消活动网络任务，恢复时不自动重新执行；尚未保存的草稿只在当前应用进程内保留。

## 本地开发

固定工具链 Flutter 3.35.7（Dart 3.9），Android JDK 17、SDK 36（目标 API 35）、NDK 28.1.13356709 和 Go（版本见 `go.mod`）。先在仓库根目录运行 `bash scripts/android/build-native.sh`，然后进入 `mobile` 执行 `flutter pub get`、`flutter analyze`、`flutter test` 和 `flutter build apk --release --target-platform android-arm64,android-x64`。

GitHub Actions 构建 ARM64 和 x86_64 所需的 Go 原生库，检查 Android 依赖图中不包含 TUI，运行 Flutter 组件测试、实际 Android 文件适配测试和模拟器上的真实协议交互。测试服务只绑定回环地址，不扫描或连接真实工业设备。

## 文件与 USB

应用使用系统文档选择器，文件导入到应用私有目录。不请求完整存储访问，不保存长期文档权限。附件默认上限 1 GiB，可明确调整到 8 GiB；配置集合遵循共享内核的 4 MiB 上限，普通文本查看上限 8 MiB；multipart 遵循协议内核的 4 MiB 限制。ZIP 导入保持相对路径，拒绝目录穿越、重复路径，并限制条目数和总展开大小。中断或失败会清理本次生成的未完成文件；若文档提供程序不允许清理，会给出明确错误。

USB 串口仅在用户选择设备、允许系统权限并明确打开后使用。后台或拔出设备会关闭连接，不自动重开或重放写入。实际 ARM64 手机、USB 转串口和工业设备仍需要独立硬件验收。

## 平台范围

当前交付目标是 Android APK。Dart 页面与平台接口为后续 iOS 复用保留边界；这不代表已构建、签名或验证 iOS 应用。iOS 文件选择、Go 绑定和设备访问需单独适配，Android USB Host 能力不作跨平台等同承诺。

最终验证包使用 Flutter AOT 编译与 Android 标准原生库压缩，不包含 Dart 调试 kernel。双 ABI 放在同一 APK 内，便于在 x86_64 模拟器上核对待交付文件的 SHA、安装、启动、五协议操作和截图；ARM64 真机仍是独立验收范围。

验证包复用 CI 中的临时调试签名，不是商店正式发行签名。不同构建若使用不同临时调试证书，更新安装可能需要先卸载；卸载会删除应用私有文件，操作前应自行导出需要保留的配置和历史。

当前固定 Flutter 3.35.7 使用官方支持的 Android Skia 渲染后端，debug 与普通 AOT 入口采用相同设置。API 29 软件模拟器曾在 Impeller 的原生 raster 线程崩溃；更换渲染后端后的实际运行结果需以对应构建的设备验收为准，不能用这一配置本身宣称兼容性通过。参考：[Flutter 官方 Impeller Android 配置](https://docs.flutter.dev/perf/impeller#android)。

Android 的历史数据库使用随 APK 编译打包的 SQLite CGo/Bionic 驱动；桌面仍用纯 Go 驱动，数据库文件格式和只读、事务、备份、迁移约束保持一致。应用不需要另行安装 SQLite。Android 构建逐个检查 ARM64/x86_64 依赖图，禁止混入使用 Linux 原始系统调用的 modernc SQLite/libc。

同时提供单 ARM64 AOT 包以便手机直接试用。它由 Flutter 官方分 ABI 构建，独立核验签名、压缩和对齐；x86_64 模拟器实际安装测试的仍是双 ABI 完整文件。单 ARM64 包不能据此称为同文件整机通过，实际 ARM64 手机和 USB 设备仍需硬件验收。

损坏配置的启动错误页提供“重试打开”“选择私有集合”“导入配置文件”。先校验，再记住所选集合；取消、校验失败或转入后台不会执行请求，也不会重置原文件。恢复到子目录仍保留原App私有沙箱，所选文件若已消失会明确报错，不生成示例覆盖恢复意图。
