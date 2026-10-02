# OPC UA 中文使用说明

采用 MIT 许可的原生 Go 客户端 gopcua v0.9.1。分发包不需要 Python、Rust、额外运行时或服务器插件；测试服务器只用于本机回环验收。

## 连接与安全

- 只连接配置的 `opc.tcp://主机:端口/路径`，不扫描网络、不自动跳转端点
- 默认 `Basic256Sha256` / `SignAndEncrypt`，不自动降级，不自动信任第一次看到的证书
- 必须提供已独立核实的叶证书 SHA-256 指纹 `server_cert_sha256` 或 PEM 根证书 `ca_file`；同时填写时两项都必须通过
- 验证证书有效期、主机名/IP SAN 和服务端 Application URI。发现结果只是未受信元数据，不能把发现到的指纹直接当成信任依据
- 安全连接使用已有 RSA PEM `cert_file` / `key_file`；不自动创建或配置持久凭据
- `None` 策略和模式必须成对设置，并明确 `allow_insecure: true`，只支持匿名身份，供用户选定的本机模拟器使用
- 用户名和证书身份必须加密。用户名模式使用 `auth: username`、`username`、`password`；用户证书模式使用 `auth: certificate`、`auth_cert_file`、`auth_key_file`
- 机密使用 `${env:变量名}`。集合不应保存明文密码/私钥。加密 PEM 私钥目前不支持
- 写属性、调用方法都必须确认。调用失败后不能推断设备未执行，禁止自动重放写入

```yaml
version: 1
requests:
  - id: temperature
    protocol: opcua
    action: read
    endpoint: opc.tcp://lab.example.test:4840
    timeout: 20s
    params:
      node_ids: ["ns=2;s=Temperature"]
      security_policy: Basic256Sha256
      security_mode: SignAndEncrypt
      cert_file: ${env:OPCUA_CLIENT_CERT_FILE}
      key_file: ${env:OPCUA_CLIENT_KEY_FILE}
      server_cert_sha256: ${env:OPCUA_SERVER_CERT_SHA256}
      auth: anonymous
```

指纹是叶证书 DER 的 SHA-256（64 位十六进制，允许冒号分隔），不是 PEM 文件或整个证书链的哈希。`ca_file` 使用 PEM 根证书和服务端提供的中间证书，不存在默认信任所有证书的后备路线。

## 操作

- `discover`：列出服务端端点、安全策略、模式、URI 和指纹，明确标记 `trusted: false`
- `browse` / `references`：`node_id` 默认 `i=85`，可选 `direction: forward/inverse/both`、`reference_type`、`include_subtypes`。支持 BrowseNext 分页和 continuation 清理；`max_references` 默认 1000，范围 1–100000，超过上限明确报不完整
- `read`：一个 `node_id` 或最多 256 个 `node_ids`。`attribute` 默认 Value，可使用名称或编号 1–27
- `attributes`：默认读取全部 27 种属性，可用 `attributes` 列表筛选。保留每个属性的状态码，不把不支持的属性伪装成成功
- `write`：明确 `node_id`、`attribute`、`value_type`、`value`。非 Value 属性严格限制为上游可写内建属性及其正确类型；服务端最终决定访问权限
- `method-arguments`：只读发现 `method_id`（或 `node_id`）的 InputArguments / OutputArguments，不调用方法
- `call`：明确 `object_id`、`method_id`、最多 64 个 `{type: Int32, value: 6}` 参数。检查方法状态、逐参数状态，并输出结果
- `subscribe`：真实数据变化订阅；`interval_ms` 默认 1000，范围 50–60000；`max_events` 默认 10，范围 1–100000。到达数量、取消或超时即清理订阅和会话

NodeId 支持 `i=2258`、`ns=2;s=Temperature` 及库支持的 GUID/字节串形式。父请求 timeout 限制整个操作，每次连接/请求也有边界，取消后的清理使用独立短超时。

类型：Boolean、SByte、Byte、Int16、UInt16、Int32、UInt32、Int64、UInt64、Float、Double、String、DateTime（带时区 RFC3339）、ByteString（Base64）、LocalizedText（`{text: 温度, locale: zh-CN}`）、QualifiedName（`{name: 温度, namespace: 2}`）。类型后加 `[]` 表示一维数组，最多 10000 个元素。严格拒绝越界、小数整数和非有限浮点值。64 位整数可用十进制字符串，避免 JSON 浮点精度丢失。

## 终端内交互

选择节点后 Enter 下钻、a 属性、f 正反向引用、r 读取、s 订阅、退格返回。属性表按 e 打开类型化 JSON 编辑器；节点按 c 读取方法参数并打开表单，填写所属对象 NodeId 和输入参数后还需要明确确认。F3 可编辑保存请求，F4 编辑完整 YAML，F8 取消。方法表单不会自动调用设备。

## 验证边界

单元测试覆盖类型/范围、参数、指纹/CA/主机名/URI/有效期拒绝、分页清理与写入确认。本机模拟器覆盖发现、分页浏览、属性读取、结构化值读写、测试方法、订阅和 pin 加密连接；不接触工业设备。方法自定义类型、多维数组、事件/报警、CRL/OCSP、历史值服务、PubSub 和厂商完整认证套件尚不支持。上游本身未完成的自定义类型/事件功能不算已完成能力。

订阅在瞬时通信错误后最多重连5次，每次重新发现和验证证书、恢复剩余事件数量；可设置 auto_reconnect=false，reconnect_interval_ms 调整间隔。证书、权限和节点错误不会重试，写入/调用从不重放。

现已实现F9成功连接历史、每端点无密码偏好和节点恢复；历史保存在集合旁的.tui-state.json，最多100条。F9选择后仍需连接表单确认，不自动连接。

发现结果表Enter打开连接表单，但不会把发现的未受信指纹自动填入信任配置。g打开浏览路径（Root、ns=N:名称与&转义），n复制NodeId、v读取并复制值；复制需明确确认。NodeId/Guid及其一维数组现也支持写入和方法参数。

按s启动独立后台订阅（最多16节点），可以同时浏览/读取；F10实时面板，Shift+S取消选中节点，F8取消全部。退出等待会话清理。Alt左右调整左栏，Alt上下调整结果区域，日志滚动到旧内容时不再强制跳回底部。

仍需补齐选中节点的完整浏览路径复制、证书生成向导、更多安全策略及各属性/引用/订阅同时可调布局；不能把本机模拟器通过等同于真实设备验证。

来源：[ua-client 功能清单](https://github.com/FreeOpcUa/ua-client)、[gopcua v0.9.1](https://github.com/gopcua/opcua/tree/v0.9.1)、[OPC UA 服务规范](https://reference.opcfoundation.org/Core/Part4/v105/docs/)

## 后续源码补齐：策略、身份和路径复制

现代服务策略支持 Basic256Sha256、Aes128_Sha256_RsaOaep、Aes256_Sha256_RsaPss；
三者均经过加密回环读取验证。Basic128Rsa15/Basic256已废弃，仅在
allow_legacy_security=true时允许旧设备兼容，默认拒绝。所有策略仍校验证书，
不存在自动降级。None仍需独立明确选项。

连接表单可明确“创建客户端证书”：生成RSA3072、自签名客户端证书与私钥，
Application URI默认urn:iotools:client，一年有效。只创建两个新文件，不覆盖；
可取消且不上传私钥。服务端管理员仍须信任公钥证书。程序不会替用户修改服务端信任。
Windows的真实私钥访问隔离取决于目录ACL；POSIX私有模式不代替Windows ACL。

p读取并复制浏览路径，n复制NodeId，v读取并复制Value，都先展示并明确确认。
多父节点/环/超过64层无法确定唯一路径时明确报错，不生成可能指向错误对象的路径。
