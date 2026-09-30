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

## 32个上游动作逐项核对

下表区分可执行同一设备能力、已有不同界面和仍缺工作流。存在F3参数编辑不等于
已实现上游专用弹窗，13个原生键映射也不等于32个上游动作任意重映射。

| 上游动作 | 当前入口/状态 | 仍有差异 |
|---|---|---|
| about | CLI `--version`、中文帮助 | 没有独立About弹窗 |
| pin | p，K可改键 | 同能力，原生请求级保存 |
| dump | D CSV，E标注JSON | 当前请求空间已读行；不汇总已切换请求的旧会话 |
| help | ? / F1 | 中文帮助，不能像上游帮助菜单直接运行所有动作 |
| refresh | F5 / Enter | 重新执行有界请求 |
| register_type | 选择四类请求或F3修改action | 没有单键按cycle_register_types轮换 |
| write | 配置write-register/coil/registers/coils/typed请求再执行 | 能力有写门禁；缺上游逐位/增减专用写弹窗 |
| go_to | F3 address，search-holding按原始值搜索 | 缺统一地址/标签交互搜索跳转 |
| label | l，K可改键 | 同能力 |
| custom_rule | F3/F4 rules及标注导入 | 运算/枚举/位/非连续规则已实现；缺完整专用规则编辑器 |
| columns | C显隐/顺序/宽度/过滤 | 同能力，另有M时间模式 |
| pause | F8取消，F5重新读取 | 不同语义，未保留上游同一会话暂停/继续状态 |
| word_order | F3 word_order | 四种顺序解释/编码已实现；缺单键轮换重解释 |
| unit_id | F3 unit，scan-units请求 | 有界明确目标；缺专用unit编辑/扫描弹窗 |
| inspect | C全部解释列及F2原始结果 | 缺逐寄存器详情弹窗和左右解释切换 |
| device_id | read-device-id，F2结果 | FC43/14已实现；缺专用访问级别切换面板 |
| raw_request | read-raw/write-raw + pdu_hex | 写功能码验证/确认已实现；缺专用raw弹窗 |
| graph | u16趋势列 | 缺任意解释列/规则的独立Modbus图形界面 |
| device | F3端点/串口参数；显式unit扫描 | 缺同级网络发现/串口枚举交互选择器；不自动扫描外部设备 |
| settings | F3/F4、C/K/M | 同类设置分散原生入口；部分上游显示/主题策略未移植 |
| copy_column | Ctrl-Y安全确认复制所选行 | 缺上游列选择后批量复制工作流 |
| write_logs | write_log_file + M写日志查看 | 持久JSONL尝试/结果与详情；非上游CSV，未知旧值不额外读取 |
| app_logs | F2当前事件结果 | 缺独立全应用日志/换行/导出工作流 |
| stats | 尚无专用面板 | 缺读取统计/会话数据概览 |
| sweep | sweep-holding/search-holding请求 | 有界引擎已实现；缺上游弹窗/全类型交互流程 |
| clear_session | Ctrl-L清空本次结果 | 同类本机操作；不提供只清图历史子动作 |
| cycle_config | F6环境profile、请求列表，M完整配置转换 | 不等于next_config自动加载/重连轮换 |
| panel | m矩阵、f固定项 | 缺独立标签/规则面板及cycle_panels设置 |
| page_up | PgUp表格滚动 | 只滚动已读结果，不移动设备读取地址窗口 |
| page_down | PgDn表格滚动 | 同上 |
| batch_decrease | F3 count | 有界读取数量可改；缺单键增减当前窗口 |
| batch_increase | F3 count | 同上 |

### 写日志与配置轮换的精确缺口

[上游writes_log.rs](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/writes_log.rs)
保存timestamp、unit、address、type、previous、value、function；由
[app/logs.rs](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/app/logs.rs)
依据配置名、接口类型、unit生成文件，write_log.enabled/directory决定持久化；另有查看入口。
本轮加入显式JSONL日志和M只读查看器；格式和目录策略不同，完整配置转换不会暗中创建日志。

上游next_config是文件路径；为空时可返回初始配置，dirty时先处理未保存提示，
加载完成后重置连接/启动位置。原生profile切换只做模板环境选择，不能算相同行为。
本轮完成离线导入和预览，仍不声称具备自动配置轮换。

## 验证

测试只用tcell模拟终端、临时本机文件和显式内存mock：导入/取消/预览/保存/重复ID/
外部文件冲突/运行时保存拒绝、只读状态不变、禁止凭据及隐式API、时间逐行更新、
coil CSV往返、各原始表示优先级、类型隔离、相对时间、未读/变化过滤、F8响应、
日志必须明确勾选、保存不创建日志、只读查看/返回和精确大整数。
没有访问外部工业设备、实际串口或任何来源配置指定的目标。
