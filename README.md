# iotools · 统一协议终端工具

把 HTTP 网络请求、Kafka、MQTT、Modbus、OPC UA 放进一个可移植的 TUI。
请求以 YAML 文件保存，也可以直接在终端内编辑。界面、快捷键、环境配置、
结果查看、取消操作和写入确认使用同一套交互。

运行程序不需要另外安装 Java、Python、Node、Docker、外部编辑器或上述五个
协议工具。协议服务器本身仍然需要存在；串口设备需要操作系统授予访问权限。
构建时使用 Go，发布产物是独立可执行文件。

## 下载与运行

从本仓库成功的 [GitHub Actions](https://github.com/Live-yum/iotools/actions)
运行中下载对应平台的 artifact：

- Windows x64（AMD/Intel）：iotools-windows-amd64
- Linux ARM64：iotools-linux-arm64
- Linux x64：iotools-linux-amd64

解压后的目录包含程序、示例、中文/协议说明、依赖许可证和 SHA256SUMS。
产物名称带有源码提交 SHA，请以该提交的实际测试结果为准。

Windows PowerShell：

```powershell
.\iotools.exe --init
.\iotools.exe --profile local
```

Linux：

```sh
chmod +x iotools
./iotools --init
./iotools --profile local
```

示例只连接本机地址。按 F4 修改目标地址、请求内容或新增请求。
打开程序、切换环境、选择请求都不会自动连接服务器；按 Enter 才会执行。
服务器未启动时会显示真实错误，不会偷偷用模拟数据代替。

## 常用操作

- Tab / Shift-Tab：切换请求列表、详情、结果、搜索框
- Enter / F5：执行所选请求；修改数据时弹出确认
- F2：切换协议专用视图与原始 JSON 结果
- F3：直接编辑单个请求表单；F4：完整 YAML；Ctrl-S 校验保存，Esc 放弃
- F6：环境；F7：HTTP jq/SQL/curl；F8：取消；F9：OPC UA历史；F10：独立订阅；F11：HTTP历史管理
- ? / F1：中文帮助；Ctrl-C / q：取消并退出

协议结果视图：

- HTTP：展开响应状态、头部和 JSON；F2 查看原始结果
- Kafka：主题/分区表；选择主题后 Enter 只读消费；支持注册表 Avro 编解码
- MQTT：主题树、JSON/MessagePack、h历史、g图表、/搜索、o/O展开折叠；[详细操作](docs/mqtt-history.md)
- Modbus：寄存器表/矩阵（m切换，+/-列数）、固定项、标签、趋势、快照差值
- OPC UA：Enter下钻，a属性，f引用，r读取，s订阅，c方法表单；属性表e编辑，退格返回

详细操作见 [终端交互说明](docs/tui.md)。固定项和标签可以保存回当前请求；S 保存快照，O 载入对比。
这些操作不会擅自修改服务器数据。

## 文件配置与命令行

```yaml
version: 1
profiles:
  local:
    base: http://127.0.0.1:8080
requests:
  - id: health
    name: 检查服务健康状态
    protocol: http
    action: GET
    endpoint: ${base}/health
    timeout: 10s
```

```sh
iotools --file examples/local.yaml --validate
iotools --profile local --run http-get
iotools --profile local --run mqtt-publish --allow-writes
iotools --profile production --read-only
```

Slumber v4/v5文件可直接载入并用F4原格式编辑；`--import slumber --input slumber.yml --file imported.yaml` 可导入新的原生文件（不覆盖）。还支持v3/REST/OpenAPI/Insomnia。模板、文件函数、链授权、jq和历史见[HTTP工作流中文指南](docs/http-slumber.md)。

`--history-db history.sqlite` 明确启用历史保存；F7可查询该文件。历史可能包含敏感响应，不默认写盘。CLI请求链写入还需 `--allow-chain-writes`，TUI按渲染后的真实目标逐步骤确认。

命令行输出 JSON 行。修改操作必须明确传 --allow-writes；--read-only 优先级
更高。Modbus 写入还必须明确填写 unit、address 和对应类型的值。错误类型、
小数地址、越界值、未知参数会在连接前拒绝，不会静默改写成默认地址或 false。

环境引用为 ${名称}，敏感信息引用为 ${env:环境变量名}。解析后的凭据只用于
内存中的当前操作，不会替换回 YAML。不要把真实密钥、令牌、私有端点或导出的
业务数据提交进仓库。没有后台扫描、遥测、自动更新或自动创建凭据。

## 协议与兼容性

- HTTP：常见方法、请求头、JSON/原始请求体、Basic/Bearer、校验 TLS/mTLS；
  用户 Slumber 分支的 AES/Base64 加解密和响应字段转换，见 [加解密中文说明](docs/CRYPTO.zh-CN.md)
- Kafka：主题/节点/分区/偏移/消费组/积压、主题管理、生产消费、筛选、SASL/TLS、
  Schema Registry 和 Kafka Connect REST；Avro 详见 [Kafka 文档](docs/kafka.md)
- MQTT：发布、多个通配主题订阅、单条读取、QoS 0/1/2、保留消息清理、TLS/mTLS
- Modbus：TCP、RTU、RTU-over-TCP、线圈/离散输入/寄存器读取与显式写入、周期采样
- OPC UA：端点发现、分页浏览、读取、类型化写入/方法调用、数据变化订阅、
  匿名/用户名/X509 身份和严格的加密端点证书验证

[配置参考](docs/configuration.md) · [OPC UA 安全配置](docs/opcua.md) ·
[安全说明](docs/security.md) · [逐项功能矩阵及未完成项](docs/capabilities.md)

项目正在按原仓库功能逐项集成。功能矩阵明确记录已经实现、已经验证以及仍需
补齐的能力；不能把“协议已接通”或“核心 CI 已通过”理解为所有上游功能已齐全。

## 测试与构建

```sh
go test -count=1 -timeout 5m ./...
go test -race -count=1 ./internal/...
go vet ./...
CGO_ENABLED=0 go build -trimpath -o iotools ./cmd/iotools
```

Go 版本和依赖校验值固定在 go.mod / go.sum。CI 在 Windows x64、Linux ARM64、
Linux x64 原生运行测试，生成 CGO_ENABLED=0 产物，并检查 ELF/PE 的运行时依赖。
Linux 产物是静态 ELF；Windows 只允许操作系统自带 DLL。

测试使用临时回环地址服务器，覆盖真实协议报文、配置校验、写入阻止、证书拒绝、
分页/订阅/取消、TUI 绘制/缩放/编辑/重复执行/退出等。Kafka 回环服务是 kfake
协议模拟器，不等于真实生产 Kafka 集群验收；物理串口与外部工业设备也没有被
本测试擅自连接。完整运行结果和平台产物以对应 SHA 的 Actions 记录为证据。

## 来源与许可证

功能参考：[MTUI](https://github.com/inowattio/MTUI)、
[ktea](https://github.com/jonas-grgt/ktea)、
[ua-client](https://github.com/FreeOpcUa/ua-client)、
[mqttui](https://github.com/EdJoPaTo/mqttui)、
[用户 Slumber 分支](https://github.com/Live-yum/slumber)。

本项目保留 Apache-2.0。MTUI/mqttui 使用 GPL 许可证，不能直接复制其应用源码
并声称仍是 Apache-only。这里采用独立实现与许可兼容的原生协议库；不是启动
外部程序的包装器。每个二进制产物都附带实际依赖及 Go 标准库的许可证文本。
