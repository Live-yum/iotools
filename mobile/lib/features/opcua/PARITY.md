# Flutter OPC UA 功能与验收对应表

证据冻结：2026-10-01 16:56 UTC。可执行源码版本 `103853150b0d2636e5609e9b4b473174e3bceb42`。

[Android 完整验收运行](https://github.com/Live-yum/iotools/actions/runs/36889678773)成功；同SHA桌面Windows x64、Linux x64、Linux ARM64原生CI成功。Android API29 x86_64软件模拟器：117项Flutter模型/组件测试、18项Android平台测试、5项主业务整机测试、1项独立OPC整机测试、1项普通AOT验收测试全部通过；tearDownAll不算业务测试。普通AOT验收内含六个协议场景和启动恢复。来源为实际日志、Gradle XML、AOT JSON与PNG，非仅存在测试源码。

“已实现”与“已在Android逐路径执行”分别记录。下表列出专用Flutter入口及可核对证据，没有把一个绿色job等同全部硬件/参数组合验证。物理ARM64、USB设备、真实工业设备、真实证书基础设施和iOS尚未验收；桌面轻量TUI保留。

| 编号 | 实际Flutter入口与实现 | 语义API与共享引擎 | 最终证据与明确边界 |
|---|---|---|---|
| U1 发现、安全、身份 | `端点` → `发现端点` → 端点卡片 → `连接与安全草稿`；策略、模式、anonymous/username/certificate、独立核对的指纹/CA、客户端/身份凭据证书和私钥字段；None/旧策略单独勾选。`_endpoints`、`_connection` | `preview`/`run`，action=`discover`；连接参数由 `opcuaOptions`/`opcuaTrust` 校验 | Android实际端点发现→None显式勾选→选择只改草稿零I/O→成功浏览通过；未知模式拒绝与不自动信任指纹由widget回归覆盖。真实证书/认证组合未全面设备验证。 |
| U2 浏览、引用、工作区 | `浏览`/`引用`；节点卡片选择、后退/前进、NodeId 跳转；`引用筛选`提供方向、引用类型、子类型、数量。`_referenceCards`、`_referenceFilter`、`_jump` | action=`browse`/`references`；使用节点和安全身份作用域缓存，显式刷新才执行 | Android实际浏览、缓存后退/前进零I/O、宽屏工作台通过；引用筛选作用域由模型/控件覆盖。 |
| U3 属性与读取 | `属性` → `刷新全部属性`；独立属性状态、类型、值、源/服务器时间；`读取此属性`和完整详情。`_attributes` | action=`attributes`/`read` → `opcua_attributes.go` | Android实际属性列表与Value/ValueRank、写后再次读取99通过；未提供的服务器时间可能显示零时间，属于显示边界而非伪造设备时间。 |
| U4 类型化写入 | `类型化写入` → 属性选择 → 标量/一维数组、LocalizedText、QualifiedName、NodeId、GUID、DateTime、ByteString 等专用控件 → 确切目标和值预览 → 确认。`_write`、`UaTypedEditor` | action=`write`；`value_type`/`value`/`attribute`，Go 再验证并执行写保护 | Android实际99预览取消零I/O、Int32越界零I/O、改回99重新预览后确认写入恰好一次并回读99通过。其他精确标量/结构/数组由widget与Go回归覆盖。 |
| U5 方法 | 方法节点卡片 → `只读取方法签名` → 按签名顺序生成类型化参数 → 确切对象/方法/参数确认 → 输出与状态。`_method`、`_methodCall` | action=`method-arguments`/`call` → `opcua_methods.go` | Android实际方法签名读取不调用、越界参数拒绝、取消不调用、确认Double(6)=12且调用计数+1通过。 |
| U6 路径与复制 | `路径与操作` → 读取唯一浏览路径、复制缓存路径、按带命名空间/转义的路径解析；显式复制 NodeId。`_actions`、`_path`、`_copy` | action=`node-path`/`browse-path` → 路径解析引擎；剪贴板为本机显式操作 | 路径解析/复制NodeId的专用控件与共享内核已实现，格式/精度/缓存语义有模型回归；本轮未执行Flutter实际路径往返，不用Go路径测试冒充整机证据。 |
| U7 独立订阅 | `订阅` → 添加当前节点，编辑间隔/次数/总时限/只读重连 → 预览确认；每项最新值、状态、时间、完整通知、按 ID 停止或停止全部。`_subscribe`、`_subscriptions` | action=`subscribe`；`subscriptions.list`/`subscriptions.stop`/`subscriptions.stop-all`，Go 独立上下文 | Android实际Temperature/Pressure双订阅收到值，同时读取属性，按ID停止一个保留另一个，再后台停止并不自动重放通过。 |
| U8 成功连接历史 | `端点` → `连接历史` → 选择并检查连接草稿；清除需确认。`_connections` | `opcua.connections`/`opcua.connections.clear`；Go 仅保存允许的连接元数据 | 成功连接历史/选择草稿/清除确认已实现，本地打开选择不联网的widget测试通过；本轮未单独完整点击成功连接历史闭环。 |
| U9 客户端身份 | `生成客户端身份` → 新证书/私钥路径和应用 URI → 目标确认；运行可取消；端点页展示生成路径、应用 URI、证书 DER SHA-256 指纹。`_identity` | `opcua.identity` → `GenerateOPCUAClientIdentityContext`；新文件独占创建，不覆盖 | 身份生成/新文件独占/DER SHA256指纹入口已实现，取消零调用与Go证书回归通过；本轮未单独在Flutter界面确认生成实际身份文件。 |
| U10 响应式、生命周期、日志 | 小屏单面板/宽屏双面板；短屏与大字号外层滚动；独立日志；读操作取消；后台关闭确认、停止任务/订阅，恢复不自动运行。`_header`、`_body`、`_logs`、生命周期观察者 | 精确 run_id 的 `cancel`、独立订阅停止；宿主负责 Go `pause`/`resume` | Android实际surface宽度变化及Flutter生命周期回调下停止/恢复不重放通过；这不等同物理系统旋转/切后台或所有设备字体。2倍字号/短屏组件检查通过。 |

实现位于本目录`opcua_workspace.dart`、`opcua_typed_editor.dart`和`opcua_controller.dart`。整机由独立`mobile/integration_test/opcua_workflow_test.dart`进程执行，8分钟上限未放宽；通过耗时7分45秒。OPC历史指成功连接历史，不增加上游未支持的Historical Access、事件订阅、自定义结构或多维参数。

完整分组报告见`docs/flutter-acceptance-103853.zh-CN.md`。本次更新仅为验收文档，不修改103853可执行源码。
