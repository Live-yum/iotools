# Flutter OPC UA：U1–U10 控件与验证映射

本表只说明 Flutter 实现与实际证据，不把旧 Java UI、Go 协议测试或测试文件存在当作 Flutter 整机通过。

## 当前证据边界

- 2026-10-01 08:07:58 UTC 冻结的 OPC 修正：9 个模型/控制器测试、10 个真实 Flutter widget 测试，共 19/19 通过；目标 Dart 分析无问题
- `bd10` 前缀候选的 Android 运行已完成真实端点发现并保存 `flutter-opc-01-discovery.png`，随后在等待 Temperature 浏览节点时失败。最终 fixture 计数为 discoveries=2、browses=0、reads=0、writes=0、calls=0，后续写入/方法/订阅断言没有执行到
- 该设备截图暴露了实际缺陷：Go 返回 `MessageSecurityModeNone`，旧表单显示 `None` 但提交原始枚举名。修正显式映射三种已知安全模式；未知值保持可见并在应用草稿前拒绝，不能默认降级到 None。修正已通过 widget 回归，仍需新候选 Android 复跑证明
- 整机入口：`mobile/integration_test/opcua_workflow_test.dart` 的 `registerOpcuaIntegrationTests()`，由总入口聚合。它使用真实 `IotoolsApp → MethodChannelEngine → JNI → Go mobileapi → 本机 OPC fixture`，不是假引擎

## 能力映射

以下函数均位于本目录的 `opcua_workspace.dart`，类型编辑器位于 `opcua_typed_editor.dart`；`opcua_controller.dart` 执行预览、单次运行、取消与事件归属。

| 编号 | 用户可操作的 Flutter 流程 | 语义 API → 现有引擎 | 已有 Flutter 证据与待执行项 |
|---|---|---|---|
| U1 发现、安全、身份 | `端点` → `发现端点` → 端点卡片 → `连接与安全草稿`；策略、模式、anonymous/username/certificate、独立核对的指纹/CA、客户端/身份凭据证书和私钥字段；None/旧策略单独勾选。`_endpoints`、`_connection` | `preview`/`run`，action=`discover`；连接参数由 `opcuaOptions`/`opcuaTrust` 校验 | Android 实际发现有截图；真实枚举名映射、未知模式拒绝、选择不联网、不自动信任指纹的 widget 测试通过。新候选连接成功待复跑 |
| U2 浏览、引用、工作区 | `浏览`/`引用`；节点卡片选择、后退/前进、NodeId 跳转；`引用筛选`提供方向、引用类型、子类型、数量。`_referenceCards`、`_referenceFilter`、`_jump` | action=`browse`/`references`；使用节点和安全身份作用域缓存，显式刷新才执行 | 缓存导航零命令、筛选只改本地、晚到结果归属、缓存上限测试通过。Android browse 在旧候选失败，修正后待跑 |
| U3 属性与读取 | `属性` → `刷新全部属性`；独立属性状态、类型、值、源/服务器时间；`读取此属性`和完整详情。`_attributes` | action=`attributes`/`read` → `opcua_attributes.go` | widget 能读取并显示属性；整机测试预期 27 属性及写后回读 99，尚未执行到 |
| U4 类型化写入 | `类型化写入` → 属性选择 → 标量/一维数组、LocalizedText、QualifiedName、NodeId、GUID、DateTime、ByteString 等专用控件 → 确切目标和值预览 → 确认。`_write`、`UaTypedEditor` | action=`write`；`value_type`/`value`/`attribute`，Go 再验证并执行写保护 | UInt64 精确文本、ByteString 与 Byte[] 区别、结构化/数组编辑、无效 Int32 零运行、取消零运行、一次确认一次运行通过。真实写 99/回读 99 待整机复跑 |
| U5 方法 | 方法节点卡片 → `只读取方法签名` → 按签名顺序生成类型化参数 → 确切对象/方法/参数确认 → 输出与状态。`_method`、`_methodCall` | action=`method-arguments`/`call` → `opcua_methods.go` | widget 签名不调用、取消不调用、确认一次、输出显示通过；真实 fixture 的 Double(6)=12、调用计数仅 +1 待复跑 |
| U6 路径与复制 | `路径与操作` → 读取唯一浏览路径、复制缓存路径、按带命名空间/转义的路径解析；显式复制 NodeId。`_actions`、`_path`、`_copy` | action=`node-path`/`browse-path` → 路径解析引擎；剪贴板为本机显式操作 | NodeId 本地格式与精度校验通过。专用控件已接线；Flutter 真实路径往返尚未执行，不能用 Go 路径测试替代 |
| U7 独立订阅 | `订阅` → 添加当前节点，编辑间隔/次数/总时限/只读重连 → 预览确认；每项最新值、状态、时间、完整通知、按 ID 停止或停止全部。`_subscribe`、`_subscriptions` | action=`subscribe`；`subscriptions.list`/`subscriptions.stop`/`subscriptions.stop-all`，Go 独立上下文 | 两个订阅共存、精确停止一个、同时读属性的 widget 测试通过。真实 Temperature/Pressure 两订阅及独立停止待复跑 |
| U8 成功连接历史 | `端点` → `连接历史` → 选择并检查连接草稿；清除需确认。`_connections` | `opcua.connections`/`opcua.connections.clear`；Go 仅保存允许的连接元数据 | 历史打开/选择只读本地、选择不联网的 widget 测试通过；实际成功连接后的历史闭环尚未整机验证 |
| U9 客户端身份 | `生成客户端身份` → 新证书/私钥路径和应用 URI → 目标确认；运行可取消；端点页展示生成路径、应用 URI、证书 DER SHA-256 指纹。`_identity` | `opcua.identity` → `GenerateOPCUAClientIdentityContext`；新文件独占创建，不覆盖 | 取消预览不调用生成 API 的 widget 测试通过；Go 的 DER 指纹且不返回私钥回归由内核测试覆盖，不能视为 Flutter 生成成功整机证据 |
| U10 响应式、生命周期、日志 | 小屏单面板/宽屏双面板；短屏与大字号外层滚动；独立日志；读操作取消；后台关闭确认、停止任务/订阅，恢复不自动运行。`_header`、`_body`、`_logs`、生命周期观察者 | 精确 run_id 的 `cancel`、独立订阅停止；宿主负责 Go `pause`/`resume` | 2× 文字短屏无溢出、后台删除确认并不重放、单次 token、晚回调隔离测试通过。整机测试的 surface resize 和人工驱动 Flutter lifecycle 回调不等于真实 OS 旋转/切后台 |

## 核心回归文件

- `mobile/test/features/opcua/opcua_models_test.dart`：精确类型、输入边界、安全模式映射、缓存作用域/上限、单次预览、只读保护、后台和晚事件
- `mobile/test/features/opcua/opcua_workspace_test.dart`：实际 Flutter 控件交互、发现信任边界、类型化写入/方法、独立订阅、历史/筛选、取消身份、大字号、未知模式拒绝
- `mobile/integration_test/opcua_workflow_test.dart`：真实 APK/协议整机流程和九张预期截图；最终必须以新候选的实际测试日志、fixture 计数与截图认定通过

## 保持的上游边界

OPC“历史”指成功连接历史；未增加 upstream 不支持的 Historical Access、事件订阅、自定义结构写入或多维参数。没有访问生产设备、真实凭据或外部工业系统。物理 Android/ARM64、真实证书基础设施与 iOS 均不能从当前模拟器或 widget 证据推定已验证。
