# Modbus 会话统计、活动日志与本机集合轮换

继续对照 MTUI
[6fc7ce35](https://github.com/inowattio/MTUI/tree/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f)，
独立实现真实工作流，不把“能在F3修改参数”当作上游专用界面已完成。

## 通信统计

Modbus结果表 `M` →“通信统计”：

- 读取成功/失败、写入成功/失败或结果未知、用户取消单独计数
- 成功读取的最小/平均/最大延迟，最后失败类别及UTC时间
- 设备端点或unit变化后重置；同一目标重复执行请求保留统计
- `c` 清空本机会话缓存、趋势、基线和统计；固定项、标签及集合文件保留
- Esc关闭视图，不停止采样；F8明确取消采样

统计从实际引擎操作测量，排除采样间隔。多次采样逐次计数；raw和设备识别按整个
逻辑操作计时；unit扫描包含每个探测。未进入通信的参数/权限校验失败只记活动状态，
不会伪造一次设备通信失败。自定义解释失败与成功的原始通信分开看待。

观察器只在TUI显式启用，不向默认CLI输出添加额外事件，也不改变通信载荷。
取消在单独计数中显示，避免把用户主动停止当成设备可靠性故障。写入报错可能表示
结果不确定，不能依靠计数自动重试。

依据上游
[CommStats字段/聚合](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/app/mod.rs)、
[读取结果/清空生命周期](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/app/lifecycle.rs)、
[统计面板](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/tui/draw_state/popups/stats.rs)。
本项目按显式请求目标定义会话，没有假装TCP每次事务的新连接是上游持久在线连接。

## 独立活动日志

`M` →“活动日志”显示Modbus开始/结束、操作结果、取消、清空和集合轮换。
它与F2原始响应、持久写操作审计日志分开：不会默认复制载荷、认证参数、端点字符串
或完整错误正文。网络错误按“超时/取消/通信或响应校验失败”记录，原始错误仍可从
单次请求结果排障。程序不收集其他协议的私密响应来补足日志。

- 最多1000条且合计预算1MiB；单条最多512个字符；超限淘汰最早条目并显示累计数量
- 默认跟随最新；Up/PgUp/Home停止跟随，f/End恢复；w切换换行，关闭换行时可左右滚动
- Ctrl-Y先显示复制预览，再明确确认；e导出打开时的日志快照到新的0600私有文件
- 文件导出不会覆盖已有文件；Esc取消不导出；F8可取消正在采样的请求
- 日志只存在当前进程内，除非用户明确复制/导出；无隐式持久化或上传

依据上游 [logger.rs](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/logger.rs)
的1000条独立日志，以及
[app/logs.rs](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/app/logs.rs)
和[日志视图](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/tui/draw_state/logs.rs)
的跟随/换行/横向滚动/复制/导出能力。原生记录为精简、安全的Modbus活动类别，不承诺
逐条复刻上游logger所有文本。

## next_config 与原生集合轮换

全局 `F12`，或 Modbus `M` →“轮换本机集合”。即使目标集合不含Modbus请求，F12仍可
返回最初集合。流程：

1. 输入下一份原生集合YAML路径；相对路径以当前集合所在目录为基准
2. 读取/校验并预览目标请求、协议、动作和端点主机摘要，选择目标profile
3. 若有临时列布局/矩阵设置，明确选择保存后切换、放弃临时设置，或取消
4. 按明确切换才更换本机请求集合；不会连接目标、启用API、执行写动作或改变只读状态

“保存为当前请求next_config”只把路径保存到当前Modbus请求，不读取目标文件。
之后打开F12/M会预填该路径；没有next_config且已离开初始集合时，预填初始集合路径。

```yaml
params:
  next_config: nearby-device.yaml
```

所有请求、后台订阅和本机工作停止后才能切换。当前源文件与预览目标均在提交时
重新核对内容和文件身份；即使新文件字节相同但被替换，也需要重新预览。
“保存设置后切换”不会用重新读取的目标偷偷替代已审阅目标。任何失败保留当前集合。
源设置保存成功、但目标后来变更的情况会阻止切换，并保留用户明确保存的设置。

文件限4MiB普通文件，拒绝直接符号链接、URL、URI、网络共享、模板与控制字符。
路径不当作服务端点，不执行文件内的模板或请求。预览隐藏URL用户信息、路径和查询值；
完整参数留在用户主动查看的请求详情。列表最多预览1000个请求，过长字段缩短显示。
切换会清除上一集合的响应、导航历史及执行中间状态，避免退格重新运行旧写操作。

上游 [config_io.rs](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/app/config_io.rs)
的next_config会加载MTUI JSON并替换连接；原生这里轮换的是统一集合YAML，且不自动连接。
MTUI JSON需要先用M完整配置转换并明确保存，不能通过next_config隐式激活API/权限。
本实现不自动跟随MTUI JSON中的路径，也没有定时轮换。

## 按键范围与未完成项

本轮补上真正可操作的统计、活动日志和集合轮换。上游32个键绑定来自
[config.rs](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/config.rs)，
派发行为见 [handler.rs](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/handler.rs)，
保留键见 [input.rs](https://github.com/inowattio/MTUI/blob/6fc7ce35f4283cbd41d77a83ebb8956c66a5be6f/src/input.rs)。
目前K仍仅映射13个已实现原生表格动作，新增工作流在M/F12；没有填入32个名字却把
所有动作指向F3。任意32动作重映射、独立规则/发现/逐位写入/地址跳转弹窗、任意解释
曲线、完整面板轮换等仍按[32动作矩阵](modbus-more.md)公开列出。

测试涵盖普通CLI输出不变、采样间隔不计入延迟、写门禁、取消类别、精确统计、日志
容量/脱敏/跟随/导出/复制预览、轮换取消/返回/临时设置/外部修改/文件替换/忙状态、
无Modbus目标集合，以及80×24终端按钮可见性。测试只使用内存mock与临时本机文件，
没有接触任何实际工业设备。
