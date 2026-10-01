# Modbus 中文操作与兼容说明

核对基准：MTUI 提交
[`6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f`](https://github.com/inowattio/MTUI/tree/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f)。
本实现独立编写；没有复制 GPL 应用源码。功能参照不意味着 MTUI 配置文件或 API
完全兼容，剩余差异见文末。

## 连接、采样与安全

- TCP：`tcp://127.0.0.1:502`；RTU-over-TCP：`rtu+tcp://127.0.0.1:502`
- 物理串口：`rtu://COM3` 或 `rtu:///dev/ttyUSB0`，配置 `baud`、`data_bits`、
  `parity`（N/E/O）、`stop_bits`
- `unit` 为 1–247；广播禁用。地址是零起始协议地址 0–65535，不是 40001 显示编号
- `count`：寄存器读取为 1–125，线圈/离散输入读取为 1–2000；读取区域不得跨过地址 65535
- `samples` 为 1–100000，`interval_ms` 为 10–86400000 毫秒；整个请求受
  `timeout` 限制。单次通信最长 5 秒；TCP/RTU-over-TCP 支持上下文取消
- 读取动作：`read-holding`、`read-input`、`read-coils`、`read-discrete`
- 写入动作：`write-register`、`write-registers`、`write-coil`、`write-coils`、`write-typed`、`read-write-registers`
- 写入必须明确提供地址和 unit；TUI 会确认，CLI 需要 `--allow-writes`。
  `--read-only` 优先禁止写入。采样次数不会让写入重复执行
- 单寄存器 `value` 和批量 `values` 必须是 0–65535 的整数；线圈使用 YAML
  `true`/`false`。不会把字符串 `"false"`、小数或错误数字悄悄转成写入值
- 多寄存器写入最多 123 个；多线圈写入最多 1968 个。仅使用用户指定目标，
  不会自动扫描网络或在连接失败时伪造结果

真实设备写入前，请按设备手册确认功能码、地址基准、unit、数据类型与字序。
本项目协议测试只连接临时回环模拟器；尚未进行物理串口或实际工业设备验收。

## 不连接设备的内置模拟器

运行 `iotools --file examples/modbus-mock.yaml`，或
`iotools --file examples/modbus-mock.yaml --run mock-registers`。
只有显式端点 `mock://local` 才启用模拟器；任何真实连接错误都不会回退到模拟。
事件流发出 `simulation` 标志。默认寄存器值等于地址，默认线圈按地址奇偶交替。
保持寄存器/线圈写入仍需确认，按 unit 隔离且仅在当前进程内保存；重启恢复默认。
输入寄存器/离散输入保持只读基准值。最多存储 262144 个修改项，无网络、串口或文件访问。
CLI 单次执行结束即丢弃模拟修改；需要写后读取时请在同一 TUI 或 API 进程内操作。

## 配置、标签与自定义解释

以下配置只访问本机示例地址，需要先启动自己的模拟器。

```yaml
version: 1
requests:
  - id: modbus-voltage
    name: 电压与状态
    protocol: modbus
    action: read-holding
    endpoint: tcp://127.0.0.1:1502
    timeout: 30s
    params:
      unit: 1
      address: 10
      count: 8
      samples: 20
      interval_ms: 1000
      word_order: ABCD
      pins: [10, 11]
      labels: {10: 电压, 11: 状态, 12: 累计量}
      rules:
        - address: 10
          repr: u16
          ops: ["/10"]
          decimals: 1
          suffix: " V"
        - address: 11
          repr: u16
          enum: {"0": 停止}
          bits: {0: 运行, 1: 就绪, 15: 故障}
        - address: 12
          repr: f64
          next: [14, 16, 17]
          word_order: CDAB
          decimals: 3
```

`repr` 支持 u16/i16/f16/u32/i32/f32/u64/i64/f64，占用 1/2/4 个寄存器。
`next` 可指定非连续后续地址，缺省地址按前一个地址加一；地址不会溢出回绕。
规则只解释本次已经读取到的数据，不会暗中扩大设备读取范围；缺失的字显示为空。

`ops` 按顺序执行加、减、乘、除、乘方：`+2`、`-1`、`*0.1`、`/10`、`^2`，
也接受 x/X 乘号。操作数必须有限，除零在执行前拒绝。`decimals` 为 0–15。
枚举优先于位名称，位名称优先于运算；浮点枚举必须精确等于整数键，不能截断。
`prefix`/`suffix` 为显示前后缀。非有限运算结果显示“不可解释”，不会用于写入。

文件通过 F4 编辑；F3 编辑单个请求。寄存器表中的 `p` 固定、`l` 标签会保存回
当前集合；`f` 只看固定项。自定义规则可用c结构化表单编辑、同响应预览后保存；也支持YAML。

## 解释列与字序

底层事件包含 u16/i16、u8/i8 高低字节、hex/hex32、binary、ascii、f16/f32/f64、
BCD/BCD32、u32/i32、u64/i64、u32_m10k/i32_m10k。TUI 显示其中主要列，CLI JSON
保留全部结果。ASCII 非可打印字节以点号显示；BCD 非法数字不产生该解释值。
64 位整数以十进制字符串保存，避免 JSON 消费者将其转成双精度后丢失精度。
自定义数学规则使用双精度，超大 64 位整数应以原始 u64/i64 列为准。

- ABCD：字节与字顺序不变
- BADC：每个 16 位字内部交换字节
- CDAB：反转字序；64 位值会反转全部四个字
- DCBA：同时交换字节和反转字序
- 单字 u16/i16/f16 不受多字字序影响

MTUI 当前 M10K 列实际显示组合后两个 16 位字的 `高/低`，分别作有符号或无符号
解释。本实现遵循此行为，**不**把它误解成“高字乘 10000 加低字”。

## 快照、差异与本机 API

TUI 寄存器表中 `S` 保存原始寄存器 JSON 快照，`O` 打开快照并比较。
快照包含端点、unit、寄存器类型、UTC 时间和原始值；拒绝覆盖已有文件，
拒绝不同端点、unit 或类型的比较，能区分新增、删除、数值变化。
S/O JSON快照针对保持/输入寄存器；M菜单可读取MTUI/原生CSV比较所有空间，D也支持线圈/离散输入，CSV需用户核对设备和unit。

```sh
iotools --file iotools.yaml --serve-modbus modbus-voltage --listen 127.0.0.1:8082
curl -H 'Content-Type: application/json' -d '{"type":"holding","address":10,"count":2}' http://127.0.0.1:8082/read
```

此服务只绑定显式数字回环地址，不接受 localhost DNS、公网/通配地址。
拒绝非回环 Host、带 Origin 的请求以及非 JSON POST，避免网页跨站调用。
请求体最多 64 KiB，同时只执行一个设备请求；忙时返回 429。

- `POST /read`：type 为 holding/input/coil/discrete，显式 address/count；holding/input 的 count 为 1–125，coil/discrete 为 1–2000
- `POST /write`：默认返回 403。只有启动时选择明确写请求并提供 `--allow-writes`，
  才允许配置中 unit/address/count/type 内的写入；HTTP 不能改变设备或扩大授权
- API 写入 values 最多 123 项；线圈仅接受数值 0/1，不接受任意非零真值
- `unit_id` 只能等于配置 unit，不支持跨 unit 覆盖
- `GET /health` 返回 API 配置状态 `configured`，`device_present: false`；这是
  服务健康信息，不是设备在线探测，也不应被监控系统当作设备连接成功
- POST 请求未知字段、尾随第二份 JSON、小数地址与越界范围均拒绝

## 精确兼容矩阵与剩余工作

| MTUI 已有能力 | 当前状态 | 本实现差异/下一步 |
|---|---|---|
| TCP/RTU/RTU-over-TCP | 已实现 | 物理串口尚未硬件验收 |
| 实时读取、unit、暂停/恢复 | 部分等价 | 有界采样、取消与重新运行；没有同一连接状态下的原版暂停语义 |
| 所有基础数值解释 | 已实现 | TUI C面板支持全部引擎解释列的显隐/顺序/宽度；time列/M菜单支持逐行UTC/相对时间 |
| 固定项、标签、自定义规则 | 部分等价 | c结构化编辑/删除、同响应预览，运算/枚举/位/非连续字、MTUI标注导入 |
| 多面板、地址矩阵 | 部分 | 表格、固定过滤、矩阵及P标签/规则面板；未有cycle_panels配置 |
| 趋势图 | 部分 | g数值类型/规则独立图及v同响应详情；有界历史，不混合旧词 |
| 写入与写入日志 | 部分 | 显式确认及结果事件；类型反向编码及显式JSONL写日志/M查看器已实现；日志格式不兼容原版CSV |
| HTTP /read /write /health/headless | 安全收敛版 | 显式服务模式；不提供公网监听、任意 unit 覆盖、任意非零线圈转换 |
| 文件 dump 与快照/diff | 部分 | 原始JSON快照、各空间已读数据CSV导出和CSV离线比较已实现；不隐式读取未采集范围 |
| 地址/标签跳转与搜索 | 部分 | /地址/相对地址/唯一标签跳转；B四空间有限扫描；search-holding原始值搜索 |
| 设备识别 | 部分 | i访问级别/对象选择、明确读取与专用对象表 |
| unit 扫描、TCP 发现 | 已实现有界版本 | U明确最多32单元；V设备选择/只读串口枚举/明确TCP目标；原生IPv4 ICMP（受OS权限限制），见[设备说明](modbus-device.md) |
| raw PDU | 已有引擎 | j十进制功能码/HEX检查、预览和结果表，写门禁；未知功能码保持拒绝 |
| 配置切换、可配置快捷键/主题 | 部分 | 本应用YAML/F3/F4及K面板表格快捷键；M可离线转换完整配置的适用字段；F12/M支持next_config原生集合轮换；全部动作键映射与主题仍有差异 |
| 内置 mock 设备 | 已实现 | 显式 mock://local、进程内存状态、模拟标记、写入确认 |
| 浏览器 mock demo | 待补 | 桌面 TUI 不等于原版 WASM 页面 |

来源逐项核对：上游 `src/interpretator.rs`、`src/custom.rs`、`src/modbus.rs`、
`openapi.yaml`，以及 `src/app/` 下 device_id/discovery/unit_scan/raw/sweep/search/
config_io/dump/diff 模块。不同版本的上游可能继续增加功能；不能用 README 列表
或上述已实现子集宣称“全部功能等价”。

## 列布局与本机导入导出更新

现已增加 `C` 列显隐/顺序/宽度面板、`K` 请求级表格快捷键、`I` MTUI标注文件或粘贴
导入（先预览再保存）、`E` 标注JSON导出和 `D` 已读取寄存器CSV导出。以上均为本机
操作，不连接或写入设备；只读限制仍约束所有设备写入。
详见[Modbus TUI高级操作与精确差异](modbus-tui-advanced.md)。
补充页列出已覆盖操作、测试和未覆盖的原版差异；其他未完成能力仍按上述矩阵标注。

## 完整配置、逐行时间与CSV比较更新

`M`菜单提供完整MTUI JSON转换预览/明确追加、CSV快照离线比较和每行UTC/相对时间。
四种读取空间均可导出CSV。请阅读[具体操作及32动作源码核对表](modbus-more.md)，
区分已实现设备能力与仍缺专用工作流，不能据此声称全部功能等价。

`M`新增通信统计、独立有界活动日志；`F12`或M可预览后轮换本机集合并返回初始集合。
具体安全语义与上游差异见[Modbus会话与轮换](modbus-session.md)。

专用地址/读取设置、同响应详情与字段图、精确类型/线圈写入和FC23：见[Modbus交互操作](modbus-interactions.md)。

结构化规则、标注面板、设备标识、检查后的原始PDU和有界四空间/Unit扫描：见[Modbus协议工具](modbus-tools.md)。

设备表单、串口元数据列举及明确网络发现详见[设备与发现](modbus-device.md)。
