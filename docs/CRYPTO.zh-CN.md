# HTTP 加解密与内置编辑

## 来源和兼容范围

协议依据是用户指定的 [Live-yum/slumber](https://github.com/Live-yum/slumber/tree/895fd49d8f51194d2def64b666e7188b6afec7a7)，不是基础 Slumber：核对了 `crates/core/src/crypto/{config,engine,transform,tests}.rs` 和 `docs/native-crypto-zh.md`。Go 实现为独立实现，使用标准库，不调用 OpenSSL、Python、Node、Java 或外部编辑器。

支持 none、base64、base64url、aes-128/192/256-cbc、aes-128/192/256-ecb；AES 使用 PKCS7，密钥分别为 16/24/32 字节，CBC 的 IV 固定要求 16 字节，ECB 禁止设置 IV。材料编码 utf8/text/hex/base64；不猜测编码、不派生、不补零、不截断。输出 Base64 带 `=` 填充且不换行；URL 输出使用 `-_`，URL 解码也接受 `+/`。默认严格解码，只有显式启用选项才忽略 ASCII 空白或补充缺失填充。明文不 trim，密文不 URLDecode。没有 Salted__、salt、nonce 或认证标签封装。

CBC/ECB 用于兼容已有协议，不推荐作为新协议设计。ECB 会暴露重复块；固定 IV 和 KEY=IV 不安全。CBC/ECB 不提供完整性认证，错误密钥可能恰好产生合法填充，不能保证识别所有错误密钥。能检测的编码、长度、填充、UTF-8、JSON 错误均失败，不返回部分解密 JSON。

## 在界面中编辑

在集合编辑器直接修改 YAML，Ctrl+S 校验并保存；无需安装外部编辑器。配置与请求保存在同一集合文件。以下均为公开合成测试数据，请勿把真实密钥提交仓库。推荐通过 `${env:IOTOOLS_KEY}` 引用环境变量；环境变量由当前用户在本地设置，本工具不生成、不上传、不保存凭据。源编辑器是明确查看源文件，文件里的明文仍然可见；自行保护文件访问权限。运行结果仅留内存/输出，工具不会自动把派生明文保存到磁盘。

```yaml
version: 1
requests:
  - id: crypto-demo
    name: 加解密测试
    protocol: http
    action: POST
    endpoint: http://127.0.0.1:18080/echo?phone=13800138000
    params:
      crypto:
        am:
          algorithm: aes-128-cbc
          key: {value: '${env:IOTOOLS_KEY}', encoding: utf8}
          iv: {value: '0123456789abcdef', encoding: utf8}
          padding: pkcs7
          plaintext_encoding: utf8
          ciphertext_encoding: base64
        b64:
          algorithm: base64
          base64_decode:
            ignore_ascii_whitespace: true
            allow_missing_padding: true
      headers:
        X-Phone: '13800138000'
        Content-Type: application/json
      body: '{"phone":"13800138000"}'
      request_transforms:
        - {target: json, name: '$.phone', crypto: am, type: encrypt}
        - {target: header, name: X-Phone, crypto: am, type: encrypt}
        - {target: query, name: phone, crypto: am, type: encrypt}
      response_transform:
        - type: decrypt
          crypto: am
          paths: ['$.phone']
```

这是本机测试服务的配置，不代表真实服务接受此请求协议。公开测试密钥为 `0123456789abcdef`。POST 等写操作仍须明确确认。请求转换按顺序执行；target 为 body（整包）、json（name 为 JSONPath）、header 或 query（name 为参数名）。type 可用 encode/decode，encrypt/decrypt 限 AES。query 加密之后再做 URL 编码，`+` 不会被错误当空格。可用 `request_crypto: am` 在全部字段转换后编码整个 body；不要无意重复加密。缺失 header/query/字段会报错，不发送请求。

## 响应转换

`response_transform` 对派生视图转换，原 response 事件的状态、头和 body 不会被替换；成功后发出 transformed 事件，失败不发出部分 transformed 事件。原 body 显示会解析 JSON并保留大整数；raw_body_base64 同时保留逐字节响应内容（Base64 编码），不会被派生转换覆盖。输出可能含敏感业务明文，请保护终端和显式重定向文件。

- type: decode / decrypt / parse_json；decrypt 限 AES
- fields 为默认 scope，必须提供 paths
- 路径支持 `$`、`.field`、`[0]`、`[*]` 和 `["特殊字段"]`；不支持递归下降、筛选、切片
- skip_missing 仅跳过缺失，skip_null 仅跳过 null，skip_blank 仅跳过空白字符串；类型不匹配仍报错
- 重复命中相同转换只执行一次；冲突转换报错
- 数字不自动转字符串，解密后的字符串不会隐式解析为 JSON
- parse_json 显式解析字符串中的 JSON，随后可继续选择它的子字段
- 整包响应第一步设置 scope: body、parse: json，可设置 text_encoding: utf8-sig 去除开头 BOM；后续可继续字段转换

```yaml
response_transform:
  - {type: decode, crypto: b64, scope: body, parse: json, text_encoding: utf8-sig}
  - {type: parse_json, paths: ['$.data.list']}
  - type: decrypt
    crypto: am
    paths: ['$.data.list[*].phone']
    skip_missing: true
    skip_null: true
    skip_blank: true
```

## 验证与明确差异

`go test ./internal/crypto ./internal/engine` 覆盖 fork 的四组独立 .NET 已知答案（空串、手机号、中文、整块）、全部 AES 密钥长度/模式/传输编码往返、材料编码、非法 Base64/长度/填充、指定错误密钥向量、路径缺失/冲突、大整数、事务回滚、真实本机 HTTP 的 JSON/header/query 编码和原始/派生视图分离。CBC/ECB 测试不构成认证安全保证。

本工具采用统一 iotools YAML，而非直接加载 Slumber 集合：crypto 放在请求 params 内，环境变量用 `${env:NAME}`。目前不支持 Slumber `{{ ... }}` 模板函数语言、response() 自动登录串联、文件模板、集合导入、JaQ 查询、SQLite 控制台、完整 Slumber profiles 结构或 `--transformed` CLI 语义。不能据此宣称完整 Slumber 功能已对齐；其余差异需继续逐项实现验收。
