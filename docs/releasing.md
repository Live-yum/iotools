# v0.3.1 发布流程与验证边界

版本来自 `mobile/pubspec.yaml` 的 `0.3.1+4`。此流程只接受 `v0.3.1`，不推断后续版本的发布资格。

## 流程

1. PR 的最终候选先通过 Android、TUI、HTTP 历史/Windows 图标、完整八项 Flutter 平台验收。然后在既有 **Verified multi-platform release** 中选择候选分支，保持 publish=false，手动执行完整 dry run；不创建 tag/release。PR 同时运行发布门禁单元测试。
2. 正常审阅、CI 通过并合并到 `main` 后，在 **Verified multi-platform release** 手动运行中选择 `main`、勾选 `publish`。默认不勾选，只构建。
3. 所有任务检出同一完整提交 SHA，重新编译。任何失败、缺包、重复包、错 SHA、错架构、哈希不符或未审阅的源码变化都阻止汇总和发布。
4. 发布器再次确认该提交已进入 main、普通 main TUI CI 成功且没有该 SHA 的失败检查，再创建/核对 `v0.3.1` tag。已存在且指向其他提交的 tag、已存在的 release 均拒绝覆盖。
5. 先创建 draft，上传全部文件，逐个重新下载并核对大小/SHA256，再次确认 tag 和 CI，才转为公开 release。上传失败只留下 draft；不自动删除、替换或盲目重试。维护者应先检查失败状态再决定恢复方式。
6. 也支持由已存在的 `v0.3.1` tag push 启动，同样要求已合并 main。工作流自己的 token 创建 tag 不需要再次触发构建：已验证的全部包恰好来自此 tag 的提交。

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

## 本次候选验收和发布源码

preflight 读取候选 SHA 的最新运行，要求 Android、TUI、HTTP 历史/图标和完整八项 Flutter 平台工作流全部成功。必须的 job/步骤不能跳过，Windows 要检查实际 EXE 四种尺寸图标，iOS 必须完成原有五项 XCTest 和 1200 秒总限。不能用旧成功运行遮盖同 SHA 的新失败或 pending 运行。

正式合并提交可以引用直接父提交的验收，但只在应用、版本、锁文件、原生桥接、编译配置、测试、平台工作流和其他构建输入 Git blob/mode 完全一致时允许。v0.3.1 不排除任何文件，要求完整 Git 树完全一致，包括发布脚本和测试 harness。源码摘要、前后 Git tree SHA 和空排除清单写入 manifest。发现当前 SHA 已有必需运行失败或 pending，就停止，不退回父提交的旧绿结果。

v0.3.0 的 8db/5c66/fb719 运行不作为此版本的新代码验收。验收记录保留真实 SHA 和 run 链接；正式 19 包由最终 main/tag SHA 重建，不把父提交测试二进制改名交付。发布器在上传前验证记录及最终 SHA 检查；下载核对完整 22 资产后，公开前再次验证父提交验收、tag 和最终 SHA 检查。父验收在上传期间变红或 pending 时保留 draft，不公开 release。

不新增 KVM 权限、长期签名密钥或账户。Android 使用现有软件模拟器验收路径。iOS 五例覆盖普通启动、真实 Go ABI/生命周期、导出边界与系统选择器展示，不宣称覆盖全部协议 UI、保存重导入、签名真机和工业硬件。

TUI 原生构建、协议/单元测试和 CLI smoke 在五种目标运行；macOS Mach-O 要求单一正确架构和仅系统库。Flutter 原生 C ABI、Dart 测试、Windows 最终包 GUI 检查及各主机 Web 网关检查随新包进行。全部 Go 可执行/共享库检查嵌入 SHA 和 `go version -m` 的目标/VCS revision（Go -trimpath 不保留 -ldflags 字段）；iOS 链接 Runner 只核对嵌入 Go SHA 及现有 Mach-O/FFI/生产隔离检查，不伪称独立 Go buildinfo 支持。

Flutter 固定 `3.35.7` 与完整 SDK SHA；Go 从 go.mod 固定；Android 保留 Java 17 / Gradle 8.11.1 / NDK 28.1.13356709；iOS simulator 使用已安装的 Xcode 26.2 / 17C52。设备构建保留现有系统 Xcode 选择。托管镜像、Java patch 和系统工具可更新，不声称整个编译环境逐字节可复现。

CocoaPods 每次生成新的工程对象 ID。门禁仅归一化新增对象 ID 与无语义格式差异，保留原对象、全部构建设置、脚本、引用和数组顺序，按审阅过的 iOS/macOS 完整工程语义摘要验收；workspace 只接受确定的 Pods 引用插入。原始文件、完整 diff、原始与归一化 hash 都保存。实测来源为 dry run 37007184755 和仅配置诊断 37008357632。除此之外任何 tracked 变化都失败，不以历史工程的随机 ID 哈希冒称跨运行字节一致。

## 本地门禁测试

`python3 -m unittest discover -s scripts/release -p 'test_*.py' -v`

这些测试覆盖拒绝错 SHA、缺包/重复包、篡改哈希、错误 job、源码/编译配置变化、发布上下文、tag 冲突、跨域重定向凭据隔离和 TUI Mach-O 边界。它们不替代真实平台构建；最终以 dry run 与正式发布 run 的各任务结果为准。
