# Flutter Modbus 功能与验收对应表

证据冻结：2026-10-01 16:56 UTC。可执行源码版本 `103853150b0d2636e5609e9b4b473174e3bceb42`。

[Android 完整验收运行](https://github.com/Live-yum/iotools/actions/runs/36889678773)成功；同SHA桌面Windows x64、Linux x64、Linux ARM64原生CI成功。Android API29 x86_64软件模拟器：117项Flutter模型/组件测试、18项Android平台测试、5项主业务整机测试、1项独立OPC整机测试、1项普通AOT验收测试全部通过；tearDownAll不算业务测试。普通AOT验收内含六个协议场景和启动恢复。来源为实际日志、Gradle XML、AOT JSON与PNG，非仅存在测试源码。

“已实现”与“已在Android逐路径执行”分别记录。下表列出专用Flutter入口及可核对证据，没有把一个绿色job等同全部硬件/参数组合验证。物理ARM64、USB设备、真实工业设备、真实证书基础设施和iOS尚未验收；桌面轻量TUI保留。

| 编号/功能 | 实际Flutter入口与实现 | 最终证据与明确边界 |
|---|---|---|
| M1 连接与传输 | 操作页 → 连接参数：传输选择、明确的端点 URI、单元、总超时、连接／请求超时、请求间隔；`WS._connection` | 真实Modbus TCP端点、单元、超时表单→JNI→线协议通过；RTU-over-TCP入口/共享实现存在，Android未逐一验证该传输。 |
| M2 USB 串口 RTU | 应用公共 USB 设备、端口和授权流程；工作区保留已选的 `usb://` 连接；`WS._connection` | Android USB平台适配、明确授权和Go RTU传输已实现；本轮没有物理USB设备，授权拒绝/拔出/驱动兼容仍需硬件验收，不能用/dev路径代替。 |
| M3 四种空间与采样 | 导航中的空间选择；样本数／间隔表单；明确读取、暂停、恢复、停止；`WS._navigate`、`_sampling`、`_operations` | Android实际holding有限读取和input空间读取、停止/生命周期流程通过；其他空间及暂停恢复完整组合由内核/组件覆盖，未逐项设备执行。 |
| M4 数值与解释器 | 可配置的 Flutter 表格／矩阵和地址详情，显示十进制、十六进制、二进制、文本及共享引擎各解释列；`WS._table`、`_inspect`；Models 的精确十进制模型 | Android实际表格显示7/42、写后字值与同次响应；Int64/UInt64精度、缺失值、字序由模型和Go回归覆盖。 |
| M5 列布局、固定和标签 | 列勾选、上下排序、宽度、地址进制、时间方式、矩阵列数、仅固定筛选、标签与固定编辑；`WS._layout`、`_annotate` | Android实际布局/缓存交互不产生设备I/O；列顺序、宽度、固定和标签的边界/取消由组件回归覆盖。 |
| M6 地址窗口、空间、单元和字序 | 十进制／0x／相对值／唯一标签；上一／下一窗口；数量加减；单元、空间、字序选择；`Controller.navigate` | Android实际本地导航不发请求；地址/数量继承、边界、标签歧义和跨空间清理由组件回归覆盖。 |
| M7 详情、趋势和统计 | 地址／字段选择；精确 NOW／MIN／MAX；明确标为近似的 AVG；Flutter 趋势图；冻结／跟随和清除；`ModbusTrendDialog`、Controller 缓存 | Android实际缓存趋势视图不增加设备I/O；大整数统计、冻结、缺口与缓存上限由模型回归覆盖，长期采样未做物理设备测试。 |
| M8 类型写入与线圈写入 | 类型选择和精确文本；u16 位开关、加减 1；单线圈开关；多线圈／寄存器表单；`WS._write`；公共目标与编码确认页 | Android实际FC16取消零写、确认一次并回读通过，恢复原夹具值；精确UInt64/线圈/其他类型由控件与内核测试覆盖。 |
| M9 FC23 事务 | 分别填写写入地址／值和读取地址／数量，再查看实际功能码、范围、编码字；`WS._write('read-write-registers')` | Android实际FC23确认后单次事务读取/写入计数各+1，读回23/42通过；写数量与读数量独立。 |
| M10 规则编辑 | 地址、类型、操作数、字序、小数位、前后缀；有序算术步骤；枚举／位标签；删除／取消；共享引擎校验与同次响应预览；`modbus_rules.dart` | 原生算术/枚举/位标签规则编辑及取消不改原请求的控件回归通过；本机interpret/rules调用不访问设备。 |
| M11 注释导入导出 | 粘贴／私有文件输入；当前确切空间的导入；固定地址并集预览；按地址选择标签／规则冲突策略，默认保留；明确应用草稿；`Tools.importAnnotations` | 同空间注释导入并集、冲突策略与跨空间拒绝的组件回归通过；未在Android系统picker里逐个演示注释文件往返。 |
| M12 完整 MTUI 导入 | ID 前缀、转换提示、逐项新请求勾选、预览令牌约束的追加／保存、显示实际备份路径；`Tools.importMTUI` | MTUI完整导入的前缀/勾选/备份/token控件与Go实际备份回归通过；Android完整MTUI文件导入未单独端到端执行。 |
| M13 快照与差异 | 从完整缓存响应保存实际端点、单元、空间、run_id、时间；新文件名；加载前后快照并比较；Tools 快照方法 | 快照明确绑定started/run_id真实来源、拒绝把A设备响应标成B的控件/模型回归通过；Android双文件快照往返未单独执行。 |
| M14 CSV 导出与离线比较 | 导出当前可见缓存列；选择／粘贴源文件、地址进制及明确来源确认；可只看变化／未读取的虚拟列表；`Tools.csvDiff`、`Controller.csv` | 可见列CSV、公式防护、精确整数与缺口差异组件回归通过；Android系统新文档导出全部CSV组合未单独执行。 |
| M15 设备标识 | 读取代码、对象 ID 表单和独立的对象 ID／值结果行；`WS._deviceId`、`_eventDetails` | Android实际FC43读取设备对象0/1/2并显示iotools fixture/local-loopback/1.0通过。 |
| M16 原始 PDU | 读／写分类、PDU 十六进制、明确地址／数量；Go 解析后的功能码／范围／编码确认；请求／响应十六进制结果；`WS._raw`、`_eventDetails` | Android实际原始FC03请求/响应HEX、写伪装读拒绝、FC06取消零写与确认往返通过。 |
| M17 扫描与搜索 | 空间／搜索选择，起止地址、批量、周期、恢复、匹配值；明确预览／运行、停止、进度／失败／跳过位置；`WS._sweep` | Android实际两地址/单周期TCP范围扫描及结果统计通过；大范围/长期循环未访问外部网络或工业设备。 |
| M18 单元探测 | 明确的唯一单元列表、空间／地址／数量、首个成功即停止；区分有响应／协议异常；只把结果连接与单元应用到草稿；`WS._probe`、`_selectionResults` | Android实际单元2协议异常→单元1有响应及结果只改草稿零I/O通过。 |
| M19 网络发现 | 目标／CIDR、TCP／Ping、端口、超时、并发；展开全部目标确认；明确开始／停止；结果只创建草稿；Tools 发现方法 | 有界目标预览/TCP/Ping发现控件与共享实现已完成；Android真实网络发现/ICMP权限路径未单独执行。本次未扫描用户网络。 |
| M20 会话统计与活动 | 分别显示累计／最近操作统计；有上限且去除敏感负载的活动列表；跟随、换行、复制、导出、清除；`Tools.statistics`、`modbus_activity.dart` | Android实际会话累计统计与活动详情查看通过；缓存200项及不记录敏感负载的模型/实现检查通过。 |
| M21 持久写入审计 | 可选私有审计路径，真实尝试／结果读取，结果未知提示；`WS._writeAudit`、`Tools.writeLog` | Android实际私有审计路径打开失败可见，且夹具写计数不变；持久审计正常写路径另由Go测试覆盖。 |
| M22 本机 HTTP 服务 | 数字回环地址、默认只读、明确的单元／地址／数量／类型写入范围确认、启动和按 run_id 停止；`Tools.controller` | Android实际本机127.0.0.1控制器只读拒绝、取消开放写入、确认窄范围、错误单元/地址/Origin拒绝、回读及停止通过；不监听公网。 |
| M23 操作入口、帮助与集合轮转 | 四个可发现标签页和具名操作；中文帮助；公共文件／集合页负责 next_config 和返回初始集合 | 四个标签与高级工具真实Android可达、中文帮助已实现；集合轮转证据见C4，不等于所有物理文件选择场景均已测试。 |

缩写WS/Tools/Models/Controller指本目录`modbus_workspace.dart`、`modbus_tools.dart`、`modbus_models.dart`、`modbus_controller.dart`。真实整机入口：`mobile/integration_test/modbus_workflow_test.dart`及`modbus_advanced_workflow_test.dart`。使用明确回环TCP夹具与独立计数，没有扫描外部设备。

完整分组报告见`docs/flutter-acceptance-103853.zh-CN.md`。本次更新仅为验收文档，不修改103853可执行源码。
