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

待补齐：原 Slumber 集合格式直接兼容、完整 {{...}} 模板函数、response() 请求
链/自动登录、文件模板、导入格式、JaQ 查询、SQLite 控制台和原 CLI 各种输出语义。
详见 [加解密中文说明](CRYPTO.zh-CN.md)。

## ktea

来源：[jonas-grgt/ktea](https://github.com/jonas-grgt/ktea)，Apache-2.0。

已实现：多环境连接；主题/节点/分区/偏移/消费组/成员/积压；主题创建删除与配置；
消息生产、只读消费、搜索；Schema Registry 查看/注册；Kafka Connect 查看/配置；
TLS/mTLS、PLAIN/SCRAM；注册表 Avro key/value 编解码；主题表与选择消费。

待补齐：更完整的专用消费组/Schema/Connect 交互表、分区修改等精细管理界面、
上游现有 Avro/Connect 工作流逐项交互验收。外部 Avro schema references 尚不支持。
Protobuf 和 ACL 管理是上游 README 中的 TODO，不应误写为已存在功能的差距。
[Kafka/Avro 说明](kafka.md)。

## mqttui

来源：[EdJoPaTo/mqttui](https://github.com/EdJoPaTo/mqttui)，GPL-3.0 许可文本。
应用源码未复制；使用许可兼容的原生库独立实现。

已实现：发布、多主题通配订阅、QoS、保留标记、单条读取、忽略保留消息、时间戳
事件、分层主题树、精确单主题保留清理、TLS/mTLS、用户名密码。

待补齐：递归保留主题清理的预览/确认工作流、自动重连与订阅恢复、部分 CLI
等价输入输出。当前不会以危险的通配删除代替未完成的确认流程。

## MTUI

来源：[inowattio/MTUI](https://github.com/inowattio/MTUI)，GPL-2.0 许可文本。
应用源码未复制；独立实现协议和显示规则。

已实现：TCP、RTU、RTU-over-TCP；unit/地址选择；线圈、离散输入、输入/保持寄存器
读取与显式写入；周期采样/取消；字节/字序；基础及 f16/f64/BCD/M10K/64 位解释；
固定项、标签、自定义运算/枚举/位规则；寄存器表/u16 趋势；文件快照和差异对比；
仅回环绑定的 HTTP API（默认只读，写入范围明确限定，并拒绝浏览器来源）。

待补齐：矩阵/更完整布局与可配置列/快捷键、设备识别、显式有界 unit 扫描/发现、
raw PDU、寄存器 sweep/search、部分导入/导出格式与 API 细节兼容。物理串口和真实
工业设备未测试，不能以回环模拟器替代真实设备结论。

## ua-client

来源：[FreeOpcUa/ua-client](https://github.com/FreeOpcUa/ua-client)，MIT。

已实现：端点发现；安全策略/模式选择；匿名/用户名/X509；证书 pin/CA、有效期、
主机名及 Application URI 校验；浏览 continuation；Value 读写；类型化方法参数；
数据变化订阅；TUI 节点下钻/读取/订阅/返回。

待补齐：所有属性的统一读写、正反向 References 面板、LocalizedText/QualifiedName
完整编辑、重连及连接历史恢复、方法参数自动发现和部分交互。事件订阅、自定义
结构类型等上游本身未完成的能力单独标注，不伪装成已有功能。

## 证据分层

- 单元：配置、类型/范围、密钥材料、编解码、事务性变换、证书拒绝
- 界面：绘制/缩放、中文帮助滚动、表单/YAML 保存取消、重复执行、取消退出、
  主题/节点/寄存器视图、快照、凭据遮盖、外部文件修改保护
- 回环协议：HTTP、MQTT broker、Kafka kfake、Modbus TCP/RTU-over-TCP、OPC UA server
- 原生平台：Windows x64、Linux ARM64、Linux x64；Linux race；静态/系统 DLL 检查；
  带精确依赖及 Go 标准库许可证的可移植产物

完整功能融合任务仍在进行。此矩阵公开说明差距，不把尚未完成的能力算作完成。
