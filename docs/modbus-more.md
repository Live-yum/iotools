# MTUI 完整配置转换、采样时间与 CSV 对比

基准为 MTUI [6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f](https://github.com/inowattio/MTUI/tree/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f)。
依据实际实现而非 README：
[配置字段及32个动作](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/config.rs)、
[配置加载/轮换](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/app/config_io.rs)、
[按键执行](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/handler.rs)、
[CSV解析/比较](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/snapshot.rs)、
[实际dump](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/app/dump.rs)。
Go 实现独立编写，没有复制 GPL 源码。

## 入口与安全边界

Modbus 结果表按 `M` 打开更多菜单：完整配置导入、CSV快照比较、采样时间显示、写日志查看。
`M` 对应可在 `K` 修改的 `more` 动作；当前有13个原生表格动作映射。
所有新菜单都保持 Esc 只关闭本机界面，F8 取消当前采样，Ctrl-C 退出。
打开、预览、取消、保存都不会自动连接设备、启动API或更改会话只读状态。

## 完整 MTUI JSON 转换

选择“导入完整MTUI配置”，输入文件路径或粘贴JSON，再给四个新请求指定公共ID前缀。
预览中显示来源路径、转换说明和将保存的完整原生请求；Tab到“明确保存到集合”
才追加请求。默认未选中执行按钮，没有自动执行步骤。四个空间均转换，启动空间排在最前。

转换的字段：

- `device.interface`：mock、TCP、RTU-over-TCP、串口及串口参数；主机禁止嵌入凭据和变量模板
- `device.unit_id`、`word_order`、`request_timeout_ms`；设备单次超时最高5000ms，超出会在预览警告
- `startup.address/type` 与 `batch.size/anchor`：计算起始读取范围；start/middle/end和地址边界与上游窗口算法一致
- `refresh_interval_ms`：保存为采样间隔，但 `samples` 固定为1，连续采样需用户另外明确配置
- 全部四类 `registers`：pins、labels、custom rules，保持不同地址空间隔离
- `columns.visible/time_mode/address_mode/label_width/custom_width`；`bits`映射原生`binary`，宽度0保留自动宽度语义
- `matrix.columns`：0采用原生8列，超过16警告并收敛为16列
- 配置名称作为请求显示名称；ID前缀限1–64位字母、数字、下划线、短横线

**这不是丢失信息却声称完全兼容的配置加载器。** 未转换字段逐项列入预览，不保存其值：

- 独立 `connect_timeout_ms`、请求后 `request_gap_ms`；原生使用请求超时，且本次只生成单次读取
- `batch.read_full_customs/custom_by_registers`、`startup.panel`、`matrix.show_context`的不同视图/额外读取行为
- `api`：任何监听或unit覆盖均不启用，仍需显式CLI服务命令
- `read_only`：导入只读动作，不让外部配置授予当前会话写权限
- `write_log`：不会套用来源目录；可在导入表单另填原生日志文件路径并明确勾选启用，保存前再次预览。默认为空/关闭，读取请求不创建日志
- `next_config`：不读取关联路径，不隐式连接下一台设备
- `keybinds`：不把上游32个动作直接套入原生键盘作用域；使用K明确设置
- `graph`、`theme`、`display`、`cycle_*`、reconnect/change-expiry/save-position等应用策略，以及其他未知顶层字段

不能转换的已处理字段内容（无效interface、越界unit/地址/批量、未知列、重复列等）直接拒绝。
批量限1–125；unit限1–247，禁用广播。文件最多4MiB，只接受普通文件。
原始JSON和未知字段的值不会另存到集合或隐藏元数据；来源路径只在预览显示。
保存保留已有YAML注释、profiles与请求，重复ID或外部文件变化会拒绝；采样运行时禁止保存。

## 每行采样时间

`M` →“采样时间显示”可临时应用或保存绝对UTC/相对时间模式，并加入时间列。
也可以在C列面板开启 `time`。采样时间是引擎收到响应发出事件的本机时刻，
不是设备时钟。只更新该事件实际包含的地址，旧行不会被新的导出时间覆盖。

```yaml
params:
  columns:
    visible: [address, time, u16, label]
    time_mode: ago # read_at / ago
    address_mode: decimal
```

相对时间在收到数据或视图重绘时更新；没有为了时钟额外发起请求或后台轮询。
CSV总是导出绝对RFC3339Nano采样时间；`captured_utc`另外记录导出时刻。

## 线圈与CSV快照

线圈/离散输入事件现在也进入地址表，以原始u16的0/1和十六进制/二进制显示，
保留每行时间、固定项、标签、趋势和CSV导出。`D`导出当前已经读取的数据，
不扫描未读地址。CSV必含type、address、u16、time；其余列按本次显示布局。
导出仍拒绝覆盖，权限0600，文本公式防护不改变64位整数字符串精度。

`M` →“CSV快照比较”：

1. 选文件或粘贴CSV，明确无0x前缀地址的十进制/十六进制解释
2. 核对设备和unit，并勾选确认。CSV没有可信目标元数据，程序不能替用户验证来源
3. 与本次已经接收到的地址对比，显示相同/变化/未读，`f`切换仅变化

支持BOM、CRLF、仅CR换行、标准引号/逗号、多行标签、大小写寄存器类型。
原始列优先级跟随上游：u16、hex、i16、bits；另外接受原生binary列。
重复同空间同地址遵循上游最后一行覆盖；不同空间同地址永不混淆。
相对时间不会当作确定的采样时刻导入。限制4MiB、128列、262144行、地址0–65535。
线圈/离散原始值额外限制0/1。未知类型、非法原始值、错列数、坏引号会拒绝。对比面板最多渲染2000行，汇总仍统计全部解析数据。

对比是打开时的一次离线视图，不修改实时值或既有基线，不主动读取CSV中缺少的数据。
其他空间与未读地址标记“未读”，不冒充设备数据被删除。S/O的带端点/unit JSON快照
仍针对holding/input；CSV流程是另一个显式核对来源的入口。

## 本机写日志

`M` →“写日志查看”，指定JSONL文件后只读查看尾部最多2MiB/1000条记录。
显示UTC时间、attempt/result阶段、unit、地址、数量、类型、已知旧值、新值、功能和结果；
Enter查看完整操作ID、目标和错误详情。查看器不改变文件，不写设备，不自动重试。

需要记录后续设备写入时，可显式为请求配置 `write_log_file`，或在完整配置导入表单
输入新路径并勾选启用，再审阅保存。原MTUI的 `write_log.directory/enabled`不会被自动采用。
导入出的四个只读请求仅保存选项；只有后来明确执行写动作时引擎才使用该路径。
写门禁仍优先执行；日志打开或尝试记录失败会阻止设备操作。结果日志失败不能当作
设备写入未发生，必须人工核实，程序不得自动重试。

原生格式为JSONL attempt/result，操作ID关联两阶段；上游使用CSV，格式不是逐字兼容。
保留旧值/新值和结果信息；旧值没有现成观测时显示未知，不为日志额外读取设备。
没有文件轮转/删除按钮，路径需用户自行管理。日志可能包含设备地址和操作数据，应保存在私有目录。

详见[专用读取/检查/图表/写入操作](modbus-interactions.md)。

规则/设备标识/原始PDU/扫描专用入口见[Modbus协议工具](modbus-tools.md)。

## 32个上游动作逐项核对

下表区分可执行同一设备能力、已有不同界面和仍缺工作流。存在F3参数编辑不等于
已实现上游专用弹窗，当前38个原生动作也不与32个上游动作逐项等同（含不同拆分）。

| 上游动作 | 当前入口/状态 | 仍有差异 |
|---|---|---|
| about | CLI `--version`、中文帮助 | 没有独立About弹窗 |
| pin | p，K可改键 | 同能力，原生请求级保存 |
| dump | D CSV，E标注JSON | 当前请求空间已读行；不汇总已切换请求的旧会话 |
| help | ? / F1 | 中文帮助，不能像上游帮助菜单直接运行所有动作 |
| refresh | r执行当前读取；F5执行源请求 | 都是重新执行有界请求；结果表Enter现在打开详情 |
| register_type | t预览下一空间；R四空间选择 | 预览后明确应用/读取；固定四空间循环，未使用上游cycle_register_types子集 |
| write | w类型/线圈/位翻转/±1/编码预览及二次确认 | FC5/6/15/16/23；没有上游同版式位光标，整数严格拒绝溢出 |
| go_to | /十进制/十六进制/相对地址/空间前缀/唯一标签 | 当前请求标签包含匹配；未缓存地址先预览，不自动读取/静默钳制 |
| label | l，K可改键 | 同能力 |
| custom_rule | c结构化规则编辑、同响应预览、明确保存/删除 | enum/bits/ops用结构化JSON字段，界面非上游逐项列表 |
| columns | C显隐/顺序/宽度/过滤 | 同能力，另有M时间模式 |
| pause | z/F8取消，r重新读取 | 未保留上游同一会话暂停/继续状态，重新读取清历史 |
| word_order | b本机四字序重解释；R保存/读取 | 必须先停止采样；同响应解释，不混合旧邻接词 |
| unit_id | u/R单元设置，U四空间显式列表探测与结果选择 | 最多32个明确Unit；有stop-first/异常区分，未有ASCII和异常显隐 |
| inspect | v/Enter逐寄存器详情，NOW/MIN/MAX/AVG | 同屏显示各模式；左右选择图字段，多词仅同响应 |
| device_id | i访问级别/对象选择、明确读取与专用对象表 | 不在打开或切换级别时自动连接；运行中禁止重复提交 |
| raw_request | j功能码/HEX校验预览与结果表 | 已知只读/写功能码分类；未知功能码保持安全限制 |
| graph | g字段/规则独立图，跟随/冻结与本机清历史 | 有界原始响应历史，不跨缺失点连线；64位图统计为近似 |
| device | F3端点/串口参数；显式unit扫描 | 缺同级网络发现/串口枚举交互选择器；不自动扫描外部设备 |
| settings | F3/F4、C/K/M | 同类设置分散原生入口；部分上游显示/主题策略未移植 |
| copy_column | y复制可见选中列，Ctrl-Y复制所选行 | 统一安全确认，列最多1MiB；不包含未读或不可见列 |
| write_logs | write_log_file + M写日志查看 | 持久JSONL尝试/结果与详情；非上游CSV，未知旧值不额外读取 |
| app_logs | M独立Modbus活动日志 | 有界脱敏、跟随/换行/横向滚动、明确复制/导出；非所有协议完整logger |
| stats | M通信统计 | 读写成功/失败、延迟、最后错误、明确清空；取消单列 |
| sweep | B四空间、范围、批量、有限循环预览和进度 | 最多16000地址/1000轮/100000次；默认遇错停止，可显式逐地址恢复，无无限循环 |
| clear_session | X清缓存/图历史/统计；图中c只清图历史 | 本机清空，不修改设备或保存的标注 |
| cycle_config | F12/M原生集合轮换，next_config路径及初始集合返回 | 预览+临时设置保存/放弃；统一YAML，不自动加载MTUI JSON/连接 |
| panel | m矩阵、f固定项、P标签/规则面板 | 当前请求空间隔离；未有cycle_panels自动循环配置 |
| page_up | [预填前一读取窗口，PgUp滚动缓存 | 需在读取设置明确应用/读取，不因滚屏自动访问设备 |
| page_down | ]预填后一读取窗口，PgDn滚动缓存 | 同上，越界拒绝而非钳制 |
| batch_decrease | {预填减少窗口数量 | 有界1..125，预览后明确应用/读取 |
| batch_increase | }预填增加窗口数量 | 同上，禁止越界 |

### 写日志与配置轮换的精确缺口

[上游writes_log.rs](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/writes_log.rs)
保存timestamp、unit、address、type、previous、value、function；由
[app/logs.rs](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/app/logs.rs)
依据配置名、接口类型、unit生成文件，write_log.enabled/directory决定持久化；另有查看入口。
本轮加入显式JSONL日志和M只读查看器；格式和目录策略不同，完整配置转换不会暗中创建日志。

上游next_config是文件路径；为空时可返回初始配置，dirty时先处理未保存提示，
加载完成后重置连接/启动位置。原生profile切换只做模板环境选择，不能算相同行为。
现已补齐F12/M明确轮换原生集合、初始集合返回和临时布局保存/放弃；不自动连接、定时轮换或隐式加载MTUI JSON。详见[会话统计与集合轮换](modbus-session.md)。

## 验证

测试只用tcell模拟终端、临时本机文件和显式内存mock：导入/取消/预览/保存/重复ID/
外部文件冲突/运行时保存拒绝、只读状态不变、禁止凭据及隐式API、时间逐行更新、
coil CSV往返、各原始表示优先级、类型隔离、相对时间、未读/变化过滤、F8响应、
日志必须明确勾选、保存不创建日志、只读查看/返回和精确大整数。
没有访问外部工业设备、实际串口或任何来源配置指定的目标。
