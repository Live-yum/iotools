# Kafka 与 Avro 中文互操作说明

使用原生纯 Go franz-go 客户端；分发的单文件客户端不需要 Java、Docker 或外部命令行程序。Kafka 服务端仍由用户自行提供，不会内置或自动启动真实集群。

## 注册表消息

produce 独立设置 key_format / value_format：text（默认）或 avro。Avro 输入采用 Avro JSON，非空 union 必须使用带类型标签的对象；不能把任意 JSON 当作 Avro。每个 Avro 部分必须指定 key_subject / value_subject。key_version / value_version 为正整数或 latest（默认跟随当前注册版本）。只读取已有 schema，不自动注册；注册须显式执行 register-schema 并确认。

消息使用 Confluent 格式：魔数0 + 4字节大端 schema ID + 编码载荷；基本 bytes schema 按其无长度前缀惯例处理。

consume 默认 auto，普通 JSON/文本可直接读，魔数消息按内嵌 writer schema ID 解码。avro 模式严格检查格式；text 明确关闭 schema 解码。缺少注册表、未知ID、损坏或尾随载荷、非Avro schema、外部 schema references 均报错，不伪装成文本。非UTF-8文本显示 Base64 对象；Kafka null 值保持 null（tombstone）。64位整数不经浮点转换。filter 作用于解码值。

这是 writer-schema 解码，不是 reader-schema 演化；Protobuf、JSON Schema、外部 Avro schema references 暂不支持。

## 独立注册表连接

- schema_registry_url：完整HTTP(S)地址，可有基础路径，禁止嵌入凭据、查询和fragment
- schema_registry_username / schema_registry_password：Basic身份
- schema_registry_bearer：Bearer优先于Basic
- schema_registry_ca_file：额外CA
- schema_registry_cert_file / schema_registry_key_file：成对mTLS

broker 的 username/password/sasl/tls/ca_file/cert_file/key_file 独立管理。证书必须验证；注册表不自动跟随重定向。schema响应和输入消息最多4MiB，每次操作最多缓存128个writer schema；Avro块最多65536项且4MiB，超界明确失败。错误不包含认证值、注册表响应体或原始schema/消息。远端带身份连接应使用HTTPS。

所有参数使用统一集合环境变量。环境名称不创建隐藏凭据存储。

```yaml
version: 1
profiles:
  local:
    brokers: 127.0.0.1:9092
    registry: http://127.0.0.1:8081
  staging:
    brokers: kafka.example.invalid:9093
    registry: https://registry.example.invalid
requests:
  - id: avro-produce
    protocol: kafka
    action: produce
    endpoint: ${brokers}
    timeout: 15s
    params:
      topic: readings
      value_format: avro
      value_subject: readings-value
      value_version: 2
      value: '{"temperature":21}'
      schema_registry_url: ${registry}
      schema_registry_username: ${env:REGISTRY_USER}
      schema_registry_password: ${env:REGISTRY_PASSWORD}
  - id: avro-consume
    protocol: kafka
    action: consume
    endpoint: ${brokers}
    timeout: 15s
    params:
      topic: readings
      value_format: auto
      limit: 10
      schema_registry_url: ${registry}
      schema_registry_username: ${env:REGISTRY_USER}
      schema_registry_password: ${env:REGISTRY_PASSWORD}
```

## 验证范围

回环 kfake 集群和HTTP注册表测试覆盖主题管理/生产/消费、认证、schema ID、损坏载荷、long精度、独立key Avro与基本bytes。它们不会连接外部集群，也不能证明所有托管Kafka厂商均已验收。

功能对照 [ktea](https://github.com/jonas-grgt/ktea)，未复制其源码。Avro依赖 [linkedin/goavro/v2](https://github.com/linkedin/goavro)，Apache-2.0、纯Go，采用公开Schema Registry HTTP和Confluent消息格式。

## 中文 TUI 原生交互

Kafka 结果区使用专用表格，不必编辑 JSON 才能导航：

- 通用：`/` 筛选当前表格，`>` 切换排序列，`=` 反向排序，`r` 重新查询，退格返回。F2 随时切换原始结果。列表最多1000行；消息仅保留最近128条，详情最多32KiB，截断会明确标注
- 主题：Enter 或 `c` 打开消费条件；`p` 生产一条消息；`o` 读取主题配置；`l` 分区偏移；`n` 创建主题；`D` 删除主题；`i` 显示/隐藏内部主题
- 主题配置：选中非敏感配置项按 `e` 编辑；`+` 增加到指定分区总数，不能减少；服务器未返回的敏感值不能误当作空值覆盖
- 消费：选择 earliest/latest、UTC相对时间 today/yesterday/last7days 或RFC3339、逗号分隔的分区集合、键和值的包含/前缀条件以及 auto/text/avro 解码。空分区代表全部；不会加入消费者组或提交偏移。消息Enter展开完整可用详情（包含headers），Ctrl-N/P切换前后记录，Esc关闭
- 消费组：Enter查看成员、客户端和分配；`l` 查看每个主题分区的已提交/起始/末端偏移及延迟；`D` 请求删除组
- Schema Registry：subjects列表Enter查看所有版本，再Enter查看schema详情；`n` 校验并注册Avro新版本；版本行 `D` 删除该精确版本，subject行 `D` 软删除整个subject；`H` 永久删除整个subject。永久删除先要求输入完整subject，再进行写入确认；注册表通常要求已软删除
- Kafka Connect：列表显示连接器状态、类型、任务数和worker；Enter查看任务状态和故障详情；`e` 编辑完整配置；`P` 暂停；`R` 恢复；`D` 删除。必须从刷新过的列表获取完整配置才能编辑，状态页不会用空配置覆盖服务端。操作完成后按 `r` 重新查询实际状态，HTTP接受不代表异步状态已达到目标

所有修改继续经过统一可滚动确认页，执行时重新检查只读/忙碌状态；取消、导航和临时表单不会改写保存的请求文件。schema/连接器配置的TUI编辑上限64KiB，超过时拒绝打开编辑而非截断后提交。更大的合法请求可使用配置文件和CLI。

测试为tcell模拟屏幕与回环kfake/HTTP服务，覆盖组成员/延迟、schema版本、连接器任务、消息保留上限、筛选排序、编辑校验、确认/取消、只读、精确永久删除目标、多分区与时间条件。未连接外部集群。

### 功能对照范围

对照源为ktea提交 `b445493db98ba2e7a7d5e27349424196dbcebdaa` 的实际页面和操作代码，而非仅依赖README的TODO列表。该版本已具有单个schema版本删除；因此本实现也提供该操作。独立实现，没有复制上游代码。快捷键采用本项目统一布局；本项目不写系统剪贴板，不通过OSC52修改终端剪贴板。主题列表不汇总cleanup.policy（通过 `o` 查看），消费的latest表示从当前末端继续读取；它不等同于历史消息的倒序分页。多集群通过请求集合、F3/F4配置编辑和F6环境选择管理，不创建独立的Kafka配置文件。


公共剪贴板支持已补充：结果表Ctrl-Y、Kafka详情Ctrl-Y先预览再明确确认；Windows使用系统API，
其他平台使用终端OSC52，不依赖xclip/pbcopy。终端/SSH可因安全策略拒绝OSC52，程序不声称已写成功，
仍可使用粘贴/文件导出。CI只检查Windows API可用性，不读取或修改用户剪贴板。


## 最近已有消息与实时尾部
消费起始偏移可选earliest、latest、most-recent。latest从末尾等待新消息；
most-recent先获取各选定分区当前末尾，分摊消息上限选择最近窗口，只读该快照，
不混入之后新增消息。空快照立即结束；过滤无匹配也在窗口末尾结束。
start_time可限制起点。按偏移分摊，不承诺跨分区严格全局时间排序；结果仍可排序搜索，
详情Ctrl+N/P浏览相邻已收消息。取消/请求总时限仍生效；压缩日志偏移空洞或不可达broker
可能以超时结束，不把未完成读取误报完整历史。最多1024分区、100000条，不读其他主题。
