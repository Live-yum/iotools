# 五个上游仓库功能对照与验收状态

核对日期：2026-09-30。以用户指定仓库已经存在的功能为准，不把上游 TODO 当成
已经实现的功能。下表会随集成更新；“已实现”不自动等于真实设备或所有平台已
验收，平台证据必须匹配对应提交 SHA 的 Actions。

## Slumber 用户分支

来源：[Live-yum/slumber](https://github.com/Live-yum/slumber)，MIT。
加解密基准明确锁定用户分支 895fd49d8f51194d2def64b666e7188b6afec7a7，
不是只参考原版 HTTP 客户端。

已实现：

- 文件保存请求、环境变量引用、HTTP 请求/响应、CLI/TUI、TLS/mTLS/鉴权
- F3 请求表单直接编辑；F4 完整 YAML 编辑；保存校验、取消及外部修改保护
- none/Base64/Base64URL；AES-128/192/256 CBC/ECB + PKCS7
- utf8/text/hex/base64 密钥材料、严格 Base64 和显式宽松选项
- 请求整包及 JSON/header/query 字段转换；响应整包/JSONPath 字段有序转换
- parse_json、BOM、缺失/null/空白策略、冲突检测、大整数保持、失败不返回部分结果
- 原始响应与派生解密视图分离；明确编辑源文件前，预览会遮盖密钥/密码
- fork 独立 .NET 已知答案及真实回环 HTTP 报文测试

新增已实现：Slumber v4/v5 原文件加载/F4编辑；v3、REST、Insomnia、OpenAPI导入；
类型化 {{...}} 模板、response()/response_header() 链、逐步骤真实目标确认、文件模板、
Go内嵌jq/JSONPath、显式启用SQLite历史及F7只读查询。
新增源码核验并补齐：本地跨文件/标量/数组引用及按$ref位置合并、curl命令生成（默认禁触发）。
远程HTTP引用不是该fork已实现功能，不再作为差距。
已补：真正流式文件上传/下载（默认1GiB、明确上限8GiB），CLI临时覆盖、F7单次覆盖、
URL/dry-run、原始/派生正文与文件输出、状态退出码；历史F11列表/查看/逐ID确认删除。
仍有差异：导入边角格式、原CLI全部别名/Python整合、jaq与gojq
个别语义；不把这些差异掩盖成完全兼容。[HTTP工作流说明](http-slumber.md)。
详见 [加解密中文说明](CRYPTO.zh-CN.md)。

## ktea

来源：[jonas-grgt/ktea](https://github.com/jonas-grgt/ktea)，Apache-2.0。

已实现：多环境连接；主题/节点/分区/偏移/消费组/成员/积压；主题创建删除与配置；
消息生产、只读消费、搜索；Schema Registry 查看/注册；Kafka Connect 查看/配置；
TLS/mTLS、PLAIN/SCRAM；注册表 Avro key/value 编解码；主题表与选择消费。

新增：主题配置/扩分区、消费组删除、Schema版本列表/版本删除/主体软删除与显式永久
删除、Connect暂停/恢复/删除；分区和时间起点、key/value包含/前缀筛选。
专用消费组/Schema/Connect交互表与编辑表单已实现，并通过本地TUI回归；
导航历史禁止重放修改操作。外部Avro schema references
在核对的ktea源码中也未递归支持，不虚构为原仓库现成功能。Windows原生剪贴板API
可用性已由Windows CI检查，真实终端剪贴板行为与厂商集群仍未验收。
Protobuf 和 ACL 管理是上游 README 中的 TODO，不应误写为已存在功能的差距。
[Kafka/Avro 说明](kafka.md)。

## mqttui

来源：[EdJoPaTo/mqttui](https://github.com/EdJoPaTo/mqttui)，GPL-3.0 许可文本。
应用源码未复制；使用许可兼容的原生库独立实现。

已实现：发布、多主题通配订阅、QoS、保留标记、单条读取、忽略保留消息、时间戳
事件、分层主题树、精确单主题保留清理、TLS/mTLS、用户名密码。

新增已实现：递归保留消息有界预览、精确主题/载荷哈希确认、变化时拒绝清理、
可滚动确认和只读保护；断线重连、恢复全部订阅、累计数量/原超时保持。
19项本机协议/界面测试及3次race通过。协议没有原子比较删除，清理前应暂停发布者；
每主题历史表/JSON字段和字节图、MessagePack、主题搜索/展开/折叠、只删除本机旧缓存
已补，并经过fuzz/界面测试。无限采集与原CLI逐字输出兼容不作保证。[MQTT说明](mqtt.md)。

## MTUI

来源：[inowattio/MTUI](https://github.com/inowattio/MTUI)，GPL-2.0 许可文本。
应用源码未复制；独立实现协议和显示规则。

已实现：TCP、RTU、RTU-over-TCP；unit/地址选择；线圈、离散输入、输入/保持寄存器
读取与显式写入；周期采样/取消；字节/字序；基础及 f16/f64/BCD/M10K/64 位解释；
固定项、标签、自定义运算/枚举/位规则；寄存器表/u16 趋势；文件快照和差异对比；
仅回环绑定的 HTTP API（默认只读，写入范围明确限定，并拒绝浏览器来源）。

新增已实现：矩阵/列数切换、设备识别、有界显式unit列表探测、受功能码与范围约束
的raw PDU、寄存器sweep/search、MTUI寄存器标注导入/导出。
已补：解释列顺序/宽度/地址方式与39个原生动作键映射、标注导入预览/合并、整份MTUI配置
转换/预览、逐行采样时间、线圈CSV/离线CSV差异、明确启用的JSONL写日志及查看器。
已实现：地址/唯一标签跳转、独立读取设置、逐寄存器详情与字段/规则图、
精确类型/线圈写表单及FC23读写同事务、读取窗口/空间/字序操作和实际动作映射。
已实现规则编辑/缓存预览/删除、标注面板、设备ID/rawPDU、四空间有界扫描和
可选错误步进恢复、明确unit列表探测；39个真实动作可重映射。
已实现连接/串口选择、明确有界TCP发现、独立连接/请求超时与间隔、真正暂停/继续。
仅列举串口元数据，不暗中打开；网络发现先预览明确目标，不自动扫描。
原生IPv4 ICMP Ping可选（Linux/Darwin使用非特权数据报套接字，Windows使用系统API）；
系统拒绝套接字权限时明确报错，不提权或回退raw socket。API/外部集成精确格式边界另列；统计/日志/配置轮换已实现。详见[32动作表](modbus-more.md)。物理串口和真实
工业设备未测试，不能以回环模拟器替代真实设备结论。

## ua-client

来源：[FreeOpcUa/ua-client](https://github.com/FreeOpcUa/ua-client)，MIT。

已实现：端点发现；安全策略/模式选择；匿名/用户名/X509；证书 pin/CA、有效期、
主机名及 Application URI 校验；浏览 continuation；Value 读写；类型化方法参数；
数据变化订阅；TUI 节点下钻/读取/订阅/返回。

新增已实现：全部27属性、正反向References、LocalizedText/QualifiedName编辑、
方法参数只读发现与类型化确认表单、瞬时通信错误后的有界订阅恢复（不重放写入）。
新增：F9历史/每端点无密码偏好/节点恢复、端点选择表单、浏览路径、后台独立订阅/F10面板、
NodeId/Guid、Alt布局调整、日志滚动保持、剪贴板明确确认。
新增：唯一浏览路径复制、可取消且不覆盖的本机RSA身份生成、现代AES128/256安全策略；
Ctrl+U或结果视图D进入浏览/属性/引用/独立订阅四窗；大屏同显、窄屏1–4分页，
缓存和订阅在切换/关闭后保持，r明确只读刷新，e属性编辑、c方法参数入口仍走写确认。
方法签名/类型已实现并有回环验证，厂商真实方法与硬件仍需现场验收。事件订阅、自定义
结构类型等上游本身未完成的能力单独标注，不伪装成已有功能。

## 证据分层

- 单元：配置、类型/范围、密钥材料、编解码、事务性变换、证书拒绝
- 界面：绘制/缩放、中文帮助滚动、表单/YAML 保存取消、重复执行、取消退出、
  主题/节点/寄存器视图、快照、凭据遮盖、外部文件修改保护
- 回环协议：HTTP、MQTT broker、Kafka kfake、Modbus TCP/RTU-over-TCP、OPC UA server
- 原生平台：Windows x64、Linux ARM64、Linux x64；Linux race；静态/系统 DLL 检查；
  带精确依赖及 Go 标准库许可证的可移植产物

五协议统一工作流已实现；此处保留原命令/格式和未验证语义的明确边界，不承诺全部
上游实现逐字节等价。全库历史集合管理、内置多语句可写SQL及Kafka most-recent有限快照已有入口，
详见[历史管理](history-admin.md)。原CLI命令拼写与数据库schema不逐字节复刻。
Windows原生ICMP回环通过；Linux两平台原生CI因非特权socket权限拒绝跳过该一项。
厂商设备、物理串口及真实桌面终端另需验收；模拟终端图不能替代这些结论。

源码逐项核对、被纠正的非上游功能和明确兼容边界见[源码审计矩阵](upstream-code-audit.md)。
