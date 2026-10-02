# v0.3.0 发布流程与验证边界

版本来自 `mobile/pubspec.yaml` 的 `0.3.0+3`。此流程只接受 `v0.3.0`，不推断后续版本的发布资格。

## 流程

1. `feat/unified-portable-tui` 的 push 自动执行完整 dry run；不创建 tag/release。PR 同时运行发布门禁单元测试。
2. 正常审阅、CI 通过并合并到 `main` 后，在 **Verified multi-platform release** 手动运行中选择 `main`、勾选 `publish`。默认不勾选，只构建。
3. 所有任务检出同一完整提交 SHA，重新编译。任何失败、缺包、重复包、错 SHA、错架构、哈希不符或未审阅的源码变化都阻止汇总和发布。
4. 发布器再次确认该提交已进入 main、普通 main TUI CI 成功且没有该 SHA 的失败检查，再创建/核对 `v0.3.0` tag。已存在且指向其他提交的 tag、已存在的 release 均拒绝覆盖。
5. 先创建 draft，上传全部文件，逐个重新下载并核对大小/SHA256，再次确认 tag 和 CI，才转为公开 release。上传失败只留下 draft；不自动删除、替换或盲目重试。维护者应先检查失败状态再决定恢复方式。
6. 也支持由已存在的 `v0.3.0` tag push 启动，同样要求已合并 main。工作流自己的 token 创建 tag 不需要再次触发构建：已验证的全部包恰好来自此 tag 的提交。

发布任务才有 `contents: write`；其他任务只有只读权限。构建产物按目标隔离，不平铺多个 `manifest.json`。上传的 `release-evidence-*` 诊断不作为用户安装包。

## 19 个包与附加文件

- 5 TUI ZIP：Windows x64、Linux x64/ARM64、macOS Intel/Apple Silicon
- 5 Flutter 原生桌面 ZIP：上述五种目标，保留完整运行库、资源、字体和许可证
- 5 离线 Web 本机网关 ZIP：上述五种主机，包含 Flutter Web 与 Go 网关
- Android ARM64 单架构 APK、ARM64/x86_64 双架构 universal APK；保留历史构建命令、正常 `lib/main.dart` 入口和 AOT。无测试入口 APK、instrumentation APK 或 transport 分片
- iOS arm64 未签名 device Release ZIP、arm64 Debug simulator developer ZIP；后者包含测试通道，只声明编译和静态检查
- `manifest.json`、`SHA256SUMS`、中文 `RELEASE_NOTES.zh-CN.md`

Android 使用原有 Gradle debug/test 签名方式，不创建正式发行者密钥。证书 SHA256 写入 manifest；签名可能跨构建不同，不能保证覆盖旧 APK。需要卸载时先自行备份数据，不自动卸载。项目、USB、Go 和 Flutter 许可证嵌入 APK，门禁检查其存在和 Go 许可证哈希。

macOS 没有 Developer ID 公证，桌面 app 保持原有 sandbox/ad-hoc 签名。Windows 没有 Authenticode 发行者签名。iOS device 不是可直接安装的 IPA；没有使用 Apple 账户或新增凭据。不要绕过系统安全警告。

## 历史证据和本次构建

发布 preflight 从 GitHub API 校验固定历史 run、完整 SHA、workflow 路径、job 和关键步骤。整个仓库只排除明确列出的发布脚本及历史 CI/验收 harness 文件，对其他 Git blob/mode（含应用、锁文件、桥接、编译配置、构建脚本、测试及资源）做等价检查；每个被排除的实际变化也记录前后 blob。文件排除不是“同一提交”的声明。

- Android `5c66d28c18575bedd16b21c28d030e0d61437129` / run `36973280392` 的 `apk` 成功
- iOS `fb719cc73760e6bcba9216d7a56dd1de886e578d` / run `36977115253` 的 `ios` 成功
- TUI `8db14221104175fd265b4d08f51b905f0bc999a0` / run `36955539788` 的三种原有目标成功
- Desktop/Web/unsigned device 同源 `8db142…` / run `36955539838` 的七个相关 job 成功；该 run 的旧 iOS job 失败，**不算成功**，只由独立 iOS 历史记录说明后续范围

本次不重新请求或改变 KVM 权限，不运行 Android emulator。iOS simulator 不运行 XCTest。它们的历史 runtime 验收保留原 SHA，绝不把历史包重命名为最终包、或声称最终提交完整重跑。iOS 历史五例也不覆盖全部协议界面、实际文件保存重导入、签名 iPhone 和物理设备。

TUI 原生构建、协议/单元测试和 CLI smoke 在五种目标运行；macOS Mach-O 要求单一正确架构和仅系统库。Flutter 原生 C ABI、Dart 测试、Windows 最终包 GUI 检查及各主机 Web 网关检查随新包进行。全部 Go 可执行/共享库检查嵌入 SHA 和 `go version -m` 的目标/链接版本；iOS 链接 Runner 只核对嵌入 Go SHA 及现有 Mach-O/FFI/生产隔离检查，不伪称独立 Go buildinfo 支持。

Flutter 固定 `3.35.7` 与完整 SDK SHA；Go 从 go.mod 固定；Android 保留 Java 17 / Gradle 8.11.1 / NDK 28.1.13356709；iOS simulator 使用已安装的 Xcode 26.2 / 17C52。设备构建保留现有系统 Xcode 选择。托管镜像、Java patch 和系统工具可更新，不声称整个编译环境逐字节可复现。

Flutter 对 iOS pbxproj 的已知生成变化仅允许历史记录的确切前后 SHA256；保存实际生成文件、diff 与 hash。除此之外任何 tracked 变化都失败。若托管工具产生新的变化，先审阅实际证据，不能放宽门禁后冒称历史等价。

## 本地门禁测试

`python3 -m unittest discover -s scripts/release -p 'test_*.py' -v`

这些测试覆盖拒绝错 SHA、缺包/重复包、篡改哈希、错误 job、源码/编译配置变化、发布上下文、tag 冲突、跨域重定向凭据隔离和 TUI Mach-O 边界。它们不替代真实平台构建；最终以 dry run 与正式发布 run 的各任务结果为准。
