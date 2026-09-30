# 五仓库源码功能核对与后续验收

本表核对实际功能入口，不用README中的TODO代替源码。状态区分：

- 已实现：存在用户可操作入口，不只是协议库接口
- 本地验证：相应单元、模拟屏幕或回环协议测试通过；新提交原生CI另行核对
- 待补：确有实现差异，列出下一步；不是把外部设备未测试冒充代码缺失
- 上游无该功能：不虚构额外“缺项”

最近已验证的发布基线是35927b9c（Windows x64/Linux ARM64/Linux x64原生全部通过）。
本表随后续提交更新，不应将正在开发的项误认为已包含在旧产物中。

## Slumber 用户分支

基准 [895fd49](https://github.com/Live-yum/slumber/tree/895fd49d8f51194d2def64b666e7188b6afec7a7)。

| 源码入口 | 功能与当前状态 | 后续 |
|---|---|---|
| core/src/crypto/{config,engine,transform,tests}.rs | 用户分支AES128/192/256 CBC/ECB、PKCS7、Base64/Base64URL、字段顺序变换、原始/派生视图；已实现并有已知答案与回环验证 | 保留原始数据，继续回归；CBC/ECB不是认证加密 |
| core/src/render/functions.rs | 类型化模板、环境/文件、JSONPath/jq、请求链、交互prompt/select和crypto函数；已实现 | jaq/gojq正则等边缘语义不承诺逐字节一致 |
| util/src/yaml/resolve.rs | 本地跨文件、数组下标、标量引用、$ref位置合并和循环检测；已实现 | 原始文件F4编辑，不静默重写被引用文件 |
| 同上 ReferenceSource | 只有Local/File，未实现HTTP远程引用 | 不将远程引用列为本仓库真实缺项 |
| core/src/http/curl.rs、cli/src/commands/generate.rs | curl生成，默认禁止依赖请求，明确execute-triggers仍受修改授权；已实现 | 输出为POSIX命令，不执行；二进制NUL参数明确拒绝 |
| cli/src/commands/db/request.rs | 历史list/get/delete；本轮实现F11和CLI，按集合隔离、逐ID确认、删除原子性验证 | 原CLI跨集合管理/批量别名、完整db shell命令族仍需逐项对应 |
| cli/src/commands/request.rs | 配方请求、环境、鉴权、表单、响应/派生结果；现有统一配置+F3/F4可操作 | 已补临时字段/header/query/form/body/basic/bearer/URL、F7单次覆盖、dry-run和原始/派生/文件输出；完整别名/默认退出策略差异公开保留 |
| core HTTP RenderedBody/BodyStream | 有界内存正文与文件模板已实现 | 已补body_file/response_file常量缓冲上传下载；6MiB回环验证，明确大小限制与变换/依赖边界 |

Python包接口、调用外部编辑器等与“独立可移植TUI”的产品形态不同；不能把这些
外部集成包装成已经提供。请求与配置编辑本身已经内置，运行无需Python/Node。

## mqttui

基准 [afdd404](https://github.com/EdJoPaTo/mqttui/tree/afdd404f1d57d9ea5065d4853560173d5573e808)。
不复制GPL源码，独立实现。

| 源码入口 | 功能与当前状态 | 后续 |
|---|---|---|
| mqtt/connect.rs、interactive/mqtt_thread.rs | 原生连接/TLS/身份、QoS、重连/恢复订阅；已实现并回环验证 | 厂商ACL/持久化部署单列外部验证 |
| clean_retained.rs、interactive/clean_retained.rs | 递归保留消息清理；已实现有限预览、精确确认和再次校验 | MQTT3.1.1无原子比较删除；应暂停发布者 |
| interactive/mqtt_history.rs、details/table.rs | 每主题历史、收到时间/QoS/值、旧缓存条目移除；本轮补齐并本地验证 | 保留内存上限，绝不把删除缓存等同删除broker消息 |
| details/graph、payload/json_selector.rs | 数字/布尔/字符串/嵌套字段与二进制字节图；本轮实现 | 布局采用统一TUI，不声称像素一致 |
| payload/messagepack | MessagePack检测/解析；本轮独立实现严格有界解码 | 复杂复合map键明确回退二进制，不静默错误解释 |
| interactive/mod.rs | /搜索，o/O展开折叠；本轮补齐 | 用户选择匹配项，不扩大订阅范围 |
| main.rs / interactive/mod.rs | 上游publish为独立子命令，不存在任意交互发布并同时订阅 | 不额外虚构“并行发布”缺项 |

## ktea

基准 [b445493](https://github.com/jonas-grgt/ktea/tree/b445493db98ba2e7a7d5e27349424196dbcebdaa)。

- 主题/分区/节点、消费组/成员/lag、创建/配置/扩分区/删除：已有专用表和确认表单
- consume_form_page：多分区、UTC相对/RFC3339起点、key/value包含/前缀筛选，已实现
- record_details_page：原始记录/headers、详情、前后记录、搜索/排序和明确剪贴板复制，已实现
- subjects_page/table_cmdbar.go、schema_details_page：版本列表/注册/版本删除/subject软删与永久删除，已实现；永久删除需完整subject确认
- kcon_page：配置、任务状态、暂停/恢复/删除，已实现
- serdes/avro.go：schema.Value直接交给goavro，并未递归解析外部schema references；因此后者不列为上游真实已实现缺项
- 旧README把部分删除写为TODO，但实际源码已有，已按源码而非TODO补齐

目前仍需核对消费页的历史反向翻页/按上下文定位等精细语义；当前latest表示从尾部实时读取。
实际Kafka厂商集群是外部验证，不等同于这些UI实现差异。Protobuf/ACL无实现入口证据时
不声称已实现，也不假称上游已经支持。

## ua-client

基准 [ab61284](https://github.com/FreeOpcUa/ua-client/tree/ab61284f370dcab57b7e64fe4c3559a5c7792b1d)。

- tui/persist.rs：成功URL历史、每端点身份/证书路径偏好、不存密码、最后节点；已实现F9
- tui/endpoint_dialog.rs：发现端点、策略/模式/身份选择；已实现；不自动信任未核实指纹
- client.rs：分页浏览、27属性、正反向引用、方法参数、LocalizedText/QualifiedName、NodeId/Guid与数组；已实现
- client.rs resolve_browse_path：命名空间与&转义路径；已实现；路径复制本轮增加，多父节点无法唯一确定时明确报错
- 独立多节点订阅、重连恢复、单节点取消，已实现F10，真实回环+并发退出race验证
- 键盘布局尺寸调整和滚动日志保持，已实现；全部属性/引用/订阅同屏布局仍与原布局不同
- 现代AES128/256策略本轮增加并真实加密回环验证；废弃SHA1策略仅显式兼容开关
- 客户端身份生成本轮增加明确本机向导、取消、不覆盖；不自动创建/上传密钥，不自动授予服务端信任

上游未实现事件订阅和自定义类型写入，单列为上游边界，不虚构已完成。厂商X509登录/
物理设备/证书撤销服务未验证，和已实现的会话/类型功能分开说明。

## MTUI

基准 [6fc7ce3](https://github.com/inowattio/MTUI/tree/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f)，
核对config_io.rs、columns.rs、dump.rs、config.rs、input.rs；不复制GPL代码。

已有TCP/RTU/RTU-over-TCP、明确unit/地址和写门禁、各寄存器空间/类型解释、规则、
固定/标签、矩阵、趋势、快照/差异、原生模拟器、有界发现/设备识别/raw PDU、回环API。
标注文件/粘贴导入、按地址合并/确认保存、JSON/CSV导出、全解释列显隐/排序/宽度、
地址模式、12个表格动作键映射也已实现并验证。

仍需实际补齐：

第四批已补整份配置显式导入、逐行时间、线圈CSV/离线差异、明确启用的写日志及查看器。

第五批补齐会话统计、独立脱敏活动日志、F12原生集合轮换（预览/保存/放弃/初始集合返回）。

1. 其余32动作完整键映射
2. 专用类型/字段曲线、设备/规则/raw对话框
3. 写日志采用统一JSONL审计，尚非原CSV格式；详见[32动作逐项表](modbus-more.md)

下一步依次补这些入口与回环/临时文件测试。物理串口、工业设备不在未经许可的测试范围内。

### 便携模板命令边界核验

用户fork `core/src/render/functions.rs::command` 在检测到 `portable_directory()` 后
直接返回 `ExternalCommandDisabled`。iotools同样禁止模板执行外部命令，符合用户
无外部运行时要求，也对应上游便携模式本来的限制，不把它误列为普通模板函数缺失。
公开函数base64/boolean/concat/debug/env/file/float/index/integer/join/jq/json_parse/
jsonpath/lower/prompt/replace/response/response_header/select/sensitive/slice/split/string/
trim/upper以及fork加密函数都有对应原生实现；具体解析边缘仍以测试和文档限制为准。

### HTTP全局引擎设置

`config/src/lib.rs::HttpEngineConfig`与`core/src/http.rs`证明上游支持follow_redirects、
ignore_certificate_hosts和large_body_size。第六批加入有界可选重定向、精确主机TLS例外
和每次高风险运行确认；安全默认与上游不同，不是该能力缺失。large_body_size是上游
显示/存储性能阈值，不等于无限制传输；原生区分4MiB内存模式和最高8GiB文件流式模式。
跨origin头部清除、拒绝HTTPS降级、curl不导出更宽泛例外属于明确安全边界。
