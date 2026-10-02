# 原生 Modbus 设备选择与明确网络发现

基于 MTUI `6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f` 的实际代码：
[设备设置、串口列举及选择](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/app/discovery.rs)、
[TCP/ICMP 发现实现](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/app/mod.rs)。
使用独立 Go 实现，未复制 GPL 源码，未增加运行时或 CGO 依赖。

## V：设备表单

在 Modbus 结果表按 `V`，或 `M` → 设备选择与明确发现。`device` 动作可在 K 重映射。

- 接口：本机模拟、串口 RTU、Modbus TCP、RTU over TCP
- TCP：主机/IP 与端口；IPv6 输入裸地址，生成的端点自动加方括号
- 串口：列表选择或手动本机路径、波特率、数据位、N/E/O 校验、停止位
- 通用：Unit、字序、连接超时、请求超时、请求间隔、整个请求的总时限
- “预览”显示目标、Unit、读取范围、参数及只读操作性质
- 临时应用、保存设置均不连接；“明确读取”才执行当前窗口的只读请求
- 更换端点或 Unit 会清除旧标注和缓存。写入要重新经过原有精确范围及数值确认
- 未改动的源端点模板继续保留，不将其展开结果写回文件。保存检查外部文件变化
- 轮换集合会提示未保存的设备/时间/读取设置，可保存、放弃或取消

串口列表只读取 OS 元数据，不打开端口，也不改变串口配置。Linux 等 Unix 平台
读取 `/dev` 中常见 `ttyUSB/ttyACM/ttyS/ttyAMA/ttyTHS/rfcomm/cu./tty.` 字符设备
及 `serial/by-id` 别名。Windows 只读 `HKLM\HARDWARE\DEVICEMAP\SERIALCOMM`。
最多返回256条、每个 Unix 目录最多检查8192项；超出明确提示可手动输入。
枚举结果不是“端口可用/当前无占用”的证明。自定义路径不要求出现在列表中。

## 网络发现：先审查再开始

TCP 和 RTU-over-TCP 表单内点击“网络发现”。初始目标只沿用已输入主机，
不会猜测本机网段或把单个 IP 自动扩大成 /24。发现仅接受以下明确目标：

- 逗号分隔的唯一单播 IPv4/IPv6 字面地址；不执行 DNS 查询
- 对齐的 IPv4 `/24` 至 `/32` 网段；`/24..30` 排除网络/广播地址，`/31..32` 全部列出
- 最多254个 IP，拒绝空地址、重复地址、组播、未指定地址、zone 和越界网段

预览逐一列出全部 IP、目标数量、端口、每目标超时和并发。只有点击“确认开始发现”
才建立 TCP 连接。每目标超时100–2000ms、并发1–32、总时限120秒；没有重试。
不发送 Modbus 或其他协议载荷。每个成功连接立即关闭；“端口开放”不能证明服务是 Modbus。

结果可以显示开放或未连接目标。完成或取消后，在开放结果按 Enter 只把 IP/端口
填回设备表单，仍需明确读取才连接设备。发现运行期间拒绝重复启动/选用目标。
F8/“停止”取消发现；Esc 取消并返回；退出取消全部本机工作。关闭后的晚到结果被丢弃。
部分完成后取消的未返回目标保持未知，不标成关闭端口。

## 时间参数

```yaml
params:
  connect_timeout_ms: 1000  # TCP 建连阶段，1..60000
  request_timeout_ms: 2000 # TCP请求阶段，1..60000；串口最高5000
  request_gap_ms: 100      # 前一请求结束到下一请求发出，0..60000
timeout: 30s              # 整次执行原有总时限，暂停和等待也计入
```

未设置新参数时保持原有单次超时（至多5秒）。TCP/RTU-over-TCP 建连和请求超时
分别生效，且都受原总时限/取消约束。串口没有 TCP 建连阶段，其连接参数不创建额外操作。
串口请求超过5000ms直接拒绝，避免取消被长串口读取无限拖延。

请求间隔在普通采样、设备标识分页及有限 sweep/unit 子请求之间共享；不同独立执行
不共享隐藏状态。间隔和暂停等待不计入通信延迟统计；等待期间取消/暂停不会再发送下一包。
MTUI 完整配置导入现已映射这三个时间字段，超过上述原生边界时在转换预览明确提示限制；
仍只创建单次只读请求，仍不自动连接。

## 与源码的差异及验收

上游网络发现提供 ICMP Ping 和 TCP Port，固定254个 /24 主机、最多256并发。
当前实现 TCP Port 与原生IPv4 ICMP Ping，增加显式目标预览与较低有界并发。ICMP依赖OS允许非特权API，具体平台验证限制见下节。
原生串口元数据列举不等同于上游库在所有 OS 上的硬件描述/USB 属性识别；允许手动路径补充。
原生明确读取执行单次/有限采样，上游连接成功后替换持久设备状态的生命周期不同。

测试包括纯函数范围校验、拒绝前零连接、实际127.0.0.1 TCP零载荷/关闭、并发/取消无重试、
独立请求超时、请求间隔/暂停交界、无设备打开的串口元数据fixture、80×24初始按钮可见、
真实tview循环中的重复点击、取消、晚到结果、失败、选择结果只填表单、外部文件冲突和保存模板。
没有访问真实工业设备或打开物理串口；Windows/Linux ARM64 交叉构建只验证编译，不代替硬件验收。

## 原生 ICMP Ping（IPv4）

“发现方式”现在可选 `ICMP Ping`。目标展开/数量/超时/并发/开始/停止/结果选择与 TCP 共用
同一有界工作流，仍必须点击明确开始。IPv6 Ping 在任何探测前拒绝；TCP 仍支持 IPv6。
Ping 每个目标只发送一次32字节随机载荷的 Echo Request，没有 Modbus 载荷和重试。

- Linux/Darwin：仅使用 `udp4` ICMP datagram socket。绝不回退到 `ip4:icmp` 原始套接字
- Windows：只从系统目录加载 `Iphlpapi.dll`，调用原生 `IcmpCreateFile/IcmpSendEcho/IcmpCloseHandle`
- 权限不足、系统API缺失或OS不支持时立即停止并显示“非特权ICMP Ping不可用”；不提权，不改内核、系统或防火墙设置，不调用外部ping程序
- Unix 回复必须同时匹配 IPv4 来源、Echo Reply类型/code、校验和、ID、sequence及随机nonce。Linux内核改写Echo ID，因此绑定实际socket本地端口；Darwin使用发送时ID
- Windows API内部关联Echo ID，本地再检查返回数量、系统状态、来源地址、精确载荷长度与nonce。返回Data指针只允许指向本次有界回复buffer内部，绝不直接解引用系统回复给出的任意指针
- 每目标超时100–2000ms、并发1–32、目标最多254个、总时限120秒。Unix取消关闭socket；Windows同步系统调用已发出后至多等待当前每目标时限，取消不再派发新目标
- 结果只标记“ICMP可达”，不声称TCP端口开放或是Modbus设备。选用仅填回IP，保留原设备表单TCP端口；未回复/失败目标为未确认，不能据此断言设备离线

实现依据：
[Go非特权ICMP API](https://pkg.go.dev/golang.org/x/net/icmp#ListenPacket)、
[Linux ping socket ID](https://github.com/torvalds/linux/blob/v6.12/net/ipv4/ping.c)、
[Darwin datagram ICMP](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/netinet/ip_icmp.c)、
[Windows IcmpSendEcho](https://learn.microsoft.com/en-us/windows/win32/api/icmpapi/nf-icmpapi-icmpsendecho)、
[微软.NET原生ICMP ABI声明](https://github.com/dotnet/runtime/blob/main/src/libraries/Common/src/Interop/Windows/IpHlpApi/Interop.ICMP.cs)。
没有复制这些项目的实现代码。

本地验证：Linux执行环境创建非特权socket时返回明确 `permission denied`，真实127.0.0.1
回环测试仅因此skip；未请求额外权限。模拟报文、来源/ID/sequence/nonce/校验和、Windows
32/64位回复buffer边界、资源释放、取消、不可用停止以及真实tview交互测试已执行。
Windows/Darwin原生网络实测须以相应CI日志为准，交叉构建本身不能证明网络成功。
测试只在明确权限/平台不可用时skip；普通回环超时或回复校验失败会直接失败。
