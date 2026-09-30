# 中文配置参考

原生集合包含 `version: 1`、可选 `profiles`（字符串变量）、`default_profile` 和 `requests`。每个请求必须有唯一 `id`、`protocol`、`action`、`endpoint`，可选 `name`、`timeout`（默认15秒，最多24小时）、`params`。未知字段和多份 YAML 文档会被拒绝。

```yaml
version: 1
profiles:
  staging: {base: 'https://api.example.invalid'}
default_profile: staging
requests:
  - id: health
    protocol: http
    action: GET
    endpoint: ${base}/health
    timeout: 10s
    params:
      bearer: ${env:STAGING_TOKEN}
```

`--profile` 优先于 default_profile。缺失变量在连接前报错。`${...}` 不递归展开；Slumber `{{...}}` 模板见专门说明。整数接受精确十进制字符串，布尔值必须是真正 YAML true/false；不会静默截断小数或把字符串 false 当作真。URL 不能嵌入用户名密码。

## TLS 与身份

HTTP/MQTT/Kafka 的 `ca_file` 将 PEM CA 加入系统信任；`cert_file`/`key_file` 必须成对，启用 mTLS。无 skip_verify。用户名密码使用环境引用。证书路径按运行目录解析；OPC UA 使用更严格的独立[信任规则](opcua.md)。

## HTTP

端点为完整 http(s) URL；操作 GET、HEAD、OPTIONS、POST、PUT、PATCH、DELETE。参数：`headers`、字符串 `body` 或 YAML 对象 `json`、`bearer`、`username`/`password`、TLS 字段；支持 `query`、`form_urlencoded`、`form_multipart`。JSON 自动填充缺失 Content-Type。4xx/5xx 输出响应并返回失败；重定向展示但不自动跟随；正文最多4MiB。[加解密和有序变换](CRYPTO.zh-CN.md)支持直接在 F3/F4 编辑。

## Kafka

端点 `host:9092,other:9092`，可带 kafka:// 前缀。连接参数：`tls`、TLS 字段、`sasl: plain|scram-sha-256|scram-sha-512`、用户名密码。

- `topics`、`brokers`、`groups`：主题/节点/消费组信息
- `group`：group 名称；`lag`：可选 groups 列表；`offsets`：topic 的分区末尾偏移
- `create-topic`：topic、partitions（默认1）、replication_factor（默认1）
- `delete-topic`：topic；`alter-topic`：topic 和 configs（如 retention.ms）
- `produce`：topic、key、value，载荷最多4MiB
- `consume`：topic、offset=earliest/latest、limit=1..100000（默认100）、可选 filter 子串。直接诊断消费，不加入消费组、不提交偏移；到数量、超时或取消即结束

Schema Registry / Kafka Connect 使用独立 HTTP(S) 端点，不能自动用 Kafka bootstrap 地址代替：

- `schemas`、`schema`（subject、version 默认 latest）
- `register-schema`（subject、json.schema），需写入确认
- `connectors`、`connector`（connector 名称）
- `update-connector`（connector 名称及 json 配置），需写入确认

[Avro 编解码与注册表认证](kafka.md)独立于 broker 凭据。

## MQTT

端点 mqtt://、mqtts://、tcp://、ssl://、ws://、wss://。操作 publish、subscribe、read-one、preview-retained、clean-retained。基础参数 topic/topics、qos=0/1/2（默认1）、payload、retain、client_id、用户名密码、TLS、limit（默认100）、ignore_retained。订阅支持通配和多个主题；read-one 收到首条有效消息即结束。

订阅/单条读取支持有界自动重连与重新订阅，可通过 auto_reconnect=false 关闭。递归清理要求快照预览和精确确认，不能把通配符直接发布为空载荷。[完整预览/清理说明](mqtt.md)。

## Modbus

端点 tcp://、rtu+tcp://（TCP 承载带CRC的RTU帧）、rtu:///dev/ttyUSB0 或 rtu://COM3。串口需要系统设备权限，不需要额外运行时；默认9600/8/N/1，请按设备明确设置。

- 读取 read-holding/read-input/read-coils/read-discrete
- 写入 write-register（value 0..65535）、write-registers（values 最多123项）、write-coil（Boolean）、write-coils、write-typed
- unit=1..247，address=0..65535 零基地址，count=1..125；写操作必须明确 unit/address
- samples 默认1，interval_ms 默认1000、最小10；写入不会随采样次数重放
- word_order=ABCD/CDAB/BADC/DCBA；包含整数/浮点/位/ASCII/BCD/M10K 等解释
- 显式有界 scan-units、device-id、sweep-holding、search-holding、read-raw/write-raw；不自动扫描真实设备
- m 切换矩阵，+/- 调整列数；matrix_columns=1..16 设置初始矩阵列数

[类型/规则、快照、模拟器、HTTP API、导入导出及安全范围](modbus.md)。

## OPC UA

[中文 OPC UA 指南](opcua.md)说明发现、安全连接、全部属性、正反向引用、类型化编辑、方法参数发现/表单、订阅和剩余验证边界。
