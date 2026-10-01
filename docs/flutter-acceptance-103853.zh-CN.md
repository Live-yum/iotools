# iotools Flutter Android：69项功能与验收证据

证据冻结：2026-10-01 16:56 UTC。可执行源码版本 `103853150b0d2636e5609e9b4b473174e3bceb42`。

[Android 完整验收运行](https://github.com/Live-yum/iotools/actions/runs/36889678773)成功；同SHA桌面Windows x64、Linux x64、Linux ARM64原生CI成功。Android API29 x86_64软件模拟器：117项Flutter模型/组件测试、18项Android平台测试、5项主业务整机测试、1项独立OPC整机测试、1项普通AOT验收测试全部通过；tearDownAll不算业务测试。普通AOT验收内含六个协议场景和启动恢复。来源为实际日志、Gradle XML、AOT JSON与PNG，非仅存在测试源码。

“已实现”与“已在Android逐路径执行”分别记录。下表列出专用Flutter入口及可核对证据，没有把一个绿色job等同全部硬件/参数组合验证。物理ARM64、USB设备、真实工业设备、真实证书基础设施和iOS尚未验收；桌面轻量TUI保留。

## 安装文件与图标

- ARM64 APK：`iotools-flutter-arm64-103853150b0d2636e5609e9b4b473174e3bceb42-aot-test-signed.apk`
- 字节数：17,531,644；SHA256：`bcda03c2d7ea8e1190c0b3edd818321c436c4a2bbf744a10bf5091eaafee27ba`
- Android最低API26，目标API35；Flutter AOT与共享Go/SQLite随包，无需Termux或外部运行时
- 临时测试签名证书SHA256：`b66d42ecbef6ad4955c0b835a9de5564c4d5504529587bc04defc9a84448dda1`。与旧试用包不同，先导出配置；覆盖安装可能需卸载旧包
- 实际安装验收使用同次构建双ABI普通AOT文件，SHA256：`5daad26433512b0b01b1c7fb0d56d733f7f4c08289e491f972d462cd51ca89fc`。单ARM64包进行了独立结构/签名/16KiB/原图校验，未冒称在ARM64实体机运行
- 用户授权头像原图SHA256：`3527d1e406ee0f5f0d572c6f77fcf592d3cf34378bfa9dc5ec61c8fb2e87380b`；APK原字节相同。Android原生资源实际渲染圆形/圆角方形、自适应/传统四图，帽子与主体位于安全区
- 实际Manifest与DEX不含旧移动TUI、旧Java业务界面、AndroidX测试Activity、JUnit或integration_test生产组件；只保留普通MainActivity及明确受保护的系统支持组件

## 可重查的证据

`android-evidence/flutter-main-exit.txt`与`flutter-opcua-exit.txt`均为`0 0`；平台Gradle XML为18 tests/0 failures/0 errors/0 skipped，普通AOT XML为1/0/0/0。`android-evidence/aot-device/report.json`绑定安装文件hash，并记录HTTP GET/POST、MQTT、Kafka、Modbus、OPC UA与损坏配置恢复结果。OPC九张真实设备截图覆盖发现、浏览、属性、写预览、99回读、Double6→12、双订阅、宽屏、后台不重放。图标四张PNG来自Android原生Drawable实际渲染，不是概念图。

117项Flutter测试是组件/模型层；6项Flutter业务整机测试是5项主套件加独立OPC。19项Android原生测试是18项平台测试加1项AOT验收。测试层互不混算，也不把合成夹具称为生产设备验收。

## 逐项入口及覆盖

### 通用（6项）

|编号|实际Flutter入口|已有证据与未逐项实测的边界|
|---|---|---|
|C1|请求卡片搜索、五协议 chips、环境 dropdown、新建/复制/删除菜单；表单选择协议/所有动作；只有执行按钮发起请求|AOT 普通入口已实际执行五协议；Flutter 主套件验证卡片、表单与显式执行。搜索/复制等控件另有组件测试，不把全部动作目录当作已逐个联网验收。|
|C2|独立表单与整集合 YAML、底部固定保存/执行、错误 SnackBar；重载需明确确认；表单保存拒绝丢弃未保存 YAML；所有草稿仅内存，无 restorationId|Android HTTP 整机验证表单、AES参数和内存YAML秘密草稿在生命周期回调后保留、未写回文件；本地精确JSON/集合草稿/生命周期回归通过。真实中文请求字节已验证；所有设备字体与IME组合未逐一验证。|
|C3|设置→导入配置/转换；ZIP；附件；私有文件列表；系统新文档导出；1–8GiB 限额选择与停止传输；HTTP multipart 二进制文件选择|18项Android平台测试包含真实测试DocumentProvider的流式导入、ZIP、取消/清理与路径保护；Flutter文件控件测试通过。平台测试不是用户亲自操作系统选择器的全部流程，也未传输物理8GiB文件。|
|C4|设置→私有文件预览切换、next_config 下一集合、返回初始集合；启动固定根后恢复保存的相对集合；切换保留各集合独立内存草稿|普通AOT实际损坏配置恢复：取消保持原件，选择合法集合后启动，损坏文件未重置；嵌套根/兄弟集合切换由Go及Flutter回归证明。next_config全程未单独在Android点击验收。|
|C5|工作台结果/执行日志、明确取消、复制/导出/重新执行；可取回完整大结果并导出；原始与转换后响应分别标记；前台事件与订阅事件独立路由|HTTP写入取消为零、确认恰好一次；OPC双订阅按ID停止、后台不重放均实际通过。run_id迟到事件、大结果淘汰/分页由组件与内核回归覆盖；大响应全部导出未逐一整机执行。|
|C6|设置→中文操作帮助、项目许可、Flutter 依赖许可、可选择完整 Go build SHA、USB 硬件验收说明；深/浅/系统外观|真实Android页面与图标截图可读、三底栏一致；117项本地组件/模型测试及analyzer通过。帮助、版本和许可入口已实现；物理设备无障碍/字体组合未全面验收。|

### HTTP、加密与历史（14项）

|编号|实际Flutter入口|已有证据与未逐项实测的边界|
|---|---|---|
|H1|Method/URL/timeout；headers/query 原生键值编辑；Bearer/Basic 密码遮罩；高级 CA/cert/key 文件选择、跳转/TLS 参数|Flutter真实AES POST与AOT GET/POST通过，取消零写、确认一次，渲染目标/正文可审查；TLS/mTLS/代理等高级参数由共享内核测试及表单覆盖，未在Android逐组合实测。|
|H2|文本正文保留空白；JSON 精确数值编辑；URL 表单键值；multipart 文本与二进制选择，不靠原始命令JSON运行|真实AES请求的完整UTF-8字节对照独立固定向量通过；UInt64/空白/multipart文件引用由组件测试覆盖。完整multipart二进制上传未在本轮Android单独执行。|
|H3|body_file/body_stream、max_upload_bytes、response_file/max_response_bytes；文件 picker 与可取消流式传输；response-file 显示字节数/SHA256并直接导出|流式文件/未知长度/取消/越界保护通过Android平台测试；上传下载边界由Go测试覆盖。物理8GiB文件和所有HTTP流式导出组合未整机验证。|
|H4|命名编码器列表；算法 picker；AES key/IV与各自编码、ciphertext base64/base64url；Base64 空白/缺 padding 开关；none 不带AES字段；单独实验室|Android真实AES-128-CBC编辑→请求加密→响应解密通过独立公开向量；AES其他密钥长度、ECB、Base64URL等由Go向量与控件回归证明，不能称Android所有算法组合均已跑过。|
|H5|请求有序转换：添加/编辑/删除/上移；encode/decode/encrypt/decrypt、body/header/query/json；响应 body/fields、JSONPath列表、skip条件、parse_json、utf8/utf8-sig|请求encrypt和响应decrypt真实往返通过；有序编辑、JSONPath/条件/编码语义由组件与共享内核测试覆盖。所有转换组合未逐个整机运行。|
|H6|现有模板保留原生文本；执行时共享内核解析 file/response/链依赖；首次预览不读取网络依赖；运行时每个写入/动态目标仍需确认|模板/依赖链/文件引用及逐步写入确认入口已实现，共享内核回归通过；复杂链、交互与TLS例外组合未单独Android整机验证。|
|H7|全局确认、遮罩 prompt、原始类型标签 select；select 使用原始 selection_index；取消明确 false；后台清空待办并关闭执行确认|原始类型选择、遮罩prompt、取消零运行与后台关闭确认的Flutter组件回归通过；普通写确认已实际Android验证，全部模板prompt/select网络链未逐个执行。|
|H8|工具→单次变量/请求覆盖；fields/header/query/form多行键值，重复 query保留；URL/body/Bearer/Basic单次覆盖；不保存到原请求，重新执行保留本次覆盖|临时覆盖的重复键/删除/互斥/不保存语义经模型与内核测试；Android首页已验证profile变量解析，复杂依赖链临时覆盖未单独整机执行。|
|H9|工具→生成 cURL；另一个明确“执行依赖后生成 cURL”确认；敏感输出警示、复制/导出，不自动执行 shell|cURL与执行依赖后生成的独立确认入口已实现、编译通过，共享内核有回归；未单独执行Android cURL链交互，不自动运行输出shell。|
|H10|工具→jq 筛选表达式；使用 http.filter.start；任务进入共享运行/取消状态，结果为 query|jq异步任务/取消入口已接共享内核，执行时间有界；组件/内核检查通过，未单独执行Android jq取消用例。|
|H11|HTTP结果卡正文/响应头/原始分段；转换后响应单独命名；复制/完整缓存检索/长文本分页搜索/新文档导出/重新执行|真实HTTP响应、解密正文与状态截图通过；完整结果缓存、分页和导出有组件/内核回归。API29个别emoji字形显示仍存在视觉差异，不能将请求字节正确等同所有字体显示正确。|
|H12|设置中明确 opt-in HTTP历史；历史搜索、时间created_at、状态/大小、正文/转换正文/原始Base64、单项删除二次确认；只读禁止删除|Android实际开启历史、保存AES及普通请求，literal详情显示test与created_at通过；中文/二进制响应请求也运行。中文/二进制每种历史详情控件未逐个单独断言；原始数据由Go/组件回归覆盖。|
|H13|历史→SQL；多结果集表格、50行分页、本地筛选；原查询可用.tables/.schema；collection元数据列表|Android实际执行含数字、test、中文的SELECT并显示DataTable，截图可核对；.tables/.schema、多语句、分页与过滤由共享内核/组件覆盖，未逐项设备操作。|
|H14|历史→集合重命名/合并/删除/迁移与SQL变更预览；显示真实SQL/备份字节数；明确选择新备份名再执行token；只读禁用变更|集合管理与事务预览/备份/只读控件已实现；Go实际迁移、WAL备份、回滚、过期token与双驱动兼容回归通过。Android整机本轮只执行只读SELECT，未实际删除/迁移用户数据库。|

### Kafka（9项）

|编号|实际Flutter入口|已有证据与未逐项实测的边界|
|---|---|---|
|K1|Kafka表单 broker/timeout；TLS/mTLS、PLAIN/SCRAM、独立Registry URL/用户/密码/Bearer/证书|实际普通本地Kafka连接/消费通过；TLS/SASL/SCRAM与Registry认证参数已实现并有共享引擎测试，Android未逐一验证认证组合。|
|K2|topics/brokers/offsets/topic-config真实结果→“浏览实体/分区/管理操作”；搜索/分页、分区Leader/副本/ISR、分区消费草稿、配置/偏移草稿|真实Kafka记录和Schema/Connector实体浏览已在Android通过；topic/broker/partition实体模型与草稿生成有组件回归，全部管理实体未逐类整机验证。|
|K3|实体详情→准备更改配置/扩充分区/删除；新建请求动作create-topic有topic/分区/副本/键值configs；底部明确预览写入|创建/扩分区/配置/删除的原生表单与写确认已实现；内核本地broker及控件保护测试通过。Android本轮不含全部topic管理确认写入。|
|K4|groups/group/lag实体列表→成员详情/单组Lag/删除；准备Lag显式groups:[选中组]；返回只浏览缓存|组/成员/lag列表与单组作用域草稿已实现，模型及Go组查询回归通过；Android组详情与删除未单独整机执行。|
|K5|consume的topic/partition列表/offset/start_time/limit；键值contains/prefix筛选；原生表单可用earliest/latest/most-recent；显式Stop|真实earliest有限消费与普通AOT消费通过；offset/time/partition筛选由内核测试覆盖，所有起点组合未逐项Android验证。|
|K6|独立记录浏览：本地搜索、精确offset/时间/分区排序、分页；详情键/值、headers、null/tombstone、原始base64、上一/下一缓存记录、复制导出、从偏移准备草稿|Android真实两条记录浏览、原始binary key /wA=、搜索/缓存前后记录入口通过；精确数值及截断信封/完整结果处理由模型回归覆盖，超大记录淘汰未单独设备验证。|
|K7|produce原生key/value、headers/partition、text/json/avro picker、独立subject/version字段；写预览不隐式注册Schema|produce/headers/partition/Avro原生表单与明确写确认已实现，实际Avro线协议由Go测试证明；本轮Flutter设备测试不含produce成功路径，不能用消费成功替代。|
|K8|Schema列表→版本→定义；原生Avro Schema输入；准备注册、版本删除、subject软删、永久清除需完整subject文本；共享写预览|Android真实注册取消零修改、确认注册+1，版本实体浏览、subject软删+1及明确subject永久清理+1通过合成Registry计数验证。|
|K9|Connector列表→状态/tasks/config；原生键值编辑配置；准备更新/暂停/恢复/删除；动作准备会清理旧body/config残留|Android真实Connector配置编辑、取消零修改、确认更新+1、暂停+1、恢复+1及实体详情通过；删除入口已实现但本轮未执行删除成功。|

### MQTT（7项）

|编号|实际Flutter入口|已有证据与未逐项实测的边界|
|---|---|---|
|Q1|Broker/clientId/认证/TLS、topic或topics列表、QoS、limit/ignore_retained/timeout/reconnect字段；subscribe/read-one明确执行|Android与普通AOT实际读取本地broker消息通过；QoS/TLS/认证/重连参数由表单和内核覆盖，未逐一设备组合实测。|
|Q2|Publish文本/HEX/Base64、QoS/retain、精确topic与正文可读预览；取消无写|文本/HEX/Base64/QoS/retain发布和写确认已实现；共享broker回归覆盖发布语义。本轮Flutter设备用例为read-one，未单独执行publish成功。|
|Q3|原生主题树展开/折叠、搜索、精确topic历史；保留/a、a//b、a/、a的独立层级与数据|Android真实/sensors//temp/原始主题与历史视图通过；/a、a//b、a/、a分离和树操作由组件模型回归证明，不归一化或扩大订阅范围。|
|Q4|消息详情文本/HEX/Base64/JSON/MessagePack；按主题有界100条、200主题/1000条/8MiB总预算；可删除本地条目、清空、导出、跟随/冻结|Android真实消息详情/缓存入口通过；历史预算、精确删除、冻结与淘汰由组件回归覆盖。所有MessagePack/导出动作未逐一设备执行。|
|Q5|字段选择保留无效旧selector；布尔/数值/数字前缀文本/数组长度/MessagePack map项数、字节索引/长度/到达速率；缩放图、min/max、冻结/清空|路径转义、数值前缀、布尔、数组/MessagePack map计数、速率等模型回归通过，原生图表已实现；连续实际broker趋势交互未单独整机执行。|
|Q6|preview-retained结果显示有限扫描全部主题分页/搜索/数量/警示；“准备清理”携带原confirm_topics/confirm_token进入clean-retained草稿，之后仍需明确写确认|完整retained快照预览、确认token与清理草稿入口已实现，内核本地broker及组件语义回归通过；Android本轮未实际执行retained清理。|
|Q7|订阅/重连事件状态与错误可读；取消始终可见；关闭详情只停止本地刷新；后台取消、回来不重连/重放|Flutter生命周期/迟到事件与取消回归通过，订阅状态入口已实现；Android真实broker断线重连未单独执行，不能用OPC后台用例代替。|

### Modbus（23项）

|编号|实际Flutter入口|已有证据与未逐项实测的边界|
|---|---|---|
|M1|操作页 → 连接参数：传输选择、明确的端点 URI、单元、总超时、连接／请求超时、请求间隔；`WS._connection`|真实Modbus TCP端点、单元、超时表单→JNI→线协议通过；RTU-over-TCP入口/共享实现存在，Android未逐一验证该传输。|
|M2|应用公共 USB 设备、端口和授权流程；工作区保留已选的 `usb://` 连接；`WS._connection`|Android USB平台适配、明确授权和Go RTU传输已实现；本轮没有物理USB设备，授权拒绝/拔出/驱动兼容仍需硬件验收，不能用/dev路径代替。|
|M3|导航中的空间选择；样本数／间隔表单；明确读取、暂停、恢复、停止；`WS._navigate`、`_sampling`、`_operations`|Android实际holding有限读取和input空间读取、停止/生命周期流程通过；其他空间及暂停恢复完整组合由内核/组件覆盖，未逐项设备执行。|
|M4|可配置的 Flutter 表格／矩阵和地址详情，显示十进制、十六进制、二进制、文本及共享引擎各解释列；`WS._table`、`_inspect`；Models 的精确十进制模型|Android实际表格显示7/42、写后字值与同次响应；Int64/UInt64精度、缺失值、字序由模型和Go回归覆盖。|
|M5|列勾选、上下排序、宽度、地址进制、时间方式、矩阵列数、仅固定筛选、标签与固定编辑；`WS._layout`、`_annotate`|Android实际布局/缓存交互不产生设备I/O；列顺序、宽度、固定和标签的边界/取消由组件回归覆盖。|
|M6|十进制／0x／相对值／唯一标签；上一／下一窗口；数量加减；单元、空间、字序选择；`Controller.navigate`|Android实际本地导航不发请求；地址/数量继承、边界、标签歧义和跨空间清理由组件回归覆盖。|
|M7|地址／字段选择；精确 NOW／MIN／MAX；明确标为近似的 AVG；Flutter 趋势图；冻结／跟随和清除；`ModbusTrendDialog`、Controller 缓存|Android实际缓存趋势视图不增加设备I/O；大整数统计、冻结、缺口与缓存上限由模型回归覆盖，长期采样未做物理设备测试。|
|M8|类型选择和精确文本；u16 位开关、加减 1；单线圈开关；多线圈／寄存器表单；`WS._write`；公共目标与编码确认页|Android实际FC16取消零写、确认一次并回读通过，恢复原夹具值；精确UInt64/线圈/其他类型由控件与内核测试覆盖。|
|M9|分别填写写入地址／值和读取地址／数量，再查看实际功能码、范围、编码字；`WS._write('read-write-registers')`|Android实际FC23确认后单次事务读取/写入计数各+1，读回23/42通过；写数量与读数量独立。|
|M10|地址、类型、操作数、字序、小数位、前后缀；有序算术步骤；枚举／位标签；删除／取消；共享引擎校验与同次响应预览；`modbus_rules.dart`|原生算术/枚举/位标签规则编辑及取消不改原请求的控件回归通过；本机interpret/rules调用不访问设备。|
|M11|粘贴／私有文件输入；当前确切空间的导入；固定地址并集预览；按地址选择标签／规则冲突策略，默认保留；明确应用草稿；`Tools.importAnnotations`|同空间注释导入并集、冲突策略与跨空间拒绝的组件回归通过；未在Android系统picker里逐个演示注释文件往返。|
|M12|ID 前缀、转换提示、逐项新请求勾选、预览令牌约束的追加／保存、显示实际备份路径；`Tools.importMTUI`|MTUI完整导入的前缀/勾选/备份/token控件与Go实际备份回归通过；Android完整MTUI文件导入未单独端到端执行。|
|M13|从完整缓存响应保存实际端点、单元、空间、run_id、时间；新文件名；加载前后快照并比较；Tools 快照方法|快照明确绑定started/run_id真实来源、拒绝把A设备响应标成B的控件/模型回归通过；Android双文件快照往返未单独执行。|
|M14|导出当前可见缓存列；选择／粘贴源文件、地址进制及明确来源确认；可只看变化／未读取的虚拟列表；`Tools.csvDiff`、`Controller.csv`|可见列CSV、公式防护、精确整数与缺口差异组件回归通过；Android系统新文档导出全部CSV组合未单独执行。|
|M15|读取代码、对象 ID 表单和独立的对象 ID／值结果行；`WS._deviceId`、`_eventDetails`|Android实际FC43读取设备对象0/1/2并显示iotools fixture/local-loopback/1.0通过。|
|M16|读／写分类、PDU 十六进制、明确地址／数量；Go 解析后的功能码／范围／编码确认；请求／响应十六进制结果；`WS._raw`、`_eventDetails`|Android实际原始FC03请求/响应HEX、写伪装读拒绝、FC06取消零写与确认往返通过。|
|M17|空间／搜索选择，起止地址、批量、周期、恢复、匹配值；明确预览／运行、停止、进度／失败／跳过位置；`WS._sweep`|Android实际两地址/单周期TCP范围扫描及结果统计通过；大范围/长期循环未访问外部网络或工业设备。|
|M18|明确的唯一单元列表、空间／地址／数量、首个成功即停止；区分有响应／协议异常；只把结果连接与单元应用到草稿；`WS._probe`、`_selectionResults`|Android实际单元2协议异常→单元1有响应及结果只改草稿零I/O通过。|
|M19|目标／CIDR、TCP／Ping、端口、超时、并发；展开全部目标确认；明确开始／停止；结果只创建草稿；Tools 发现方法|有界目标预览/TCP/Ping发现控件与共享实现已完成；Android真实网络发现/ICMP权限路径未单独执行。本次未扫描用户网络。|
|M20|分别显示累计／最近操作统计；有上限且去除敏感负载的活动列表；跟随、换行、复制、导出、清除；`Tools.statistics`、`modbus_activity.dart`|Android实际会话累计统计与活动详情查看通过；缓存200项及不记录敏感负载的模型/实现检查通过。|
|M21|可选私有审计路径，真实尝试／结果读取，结果未知提示；`WS._writeAudit`、`Tools.writeLog`|Android实际私有审计路径打开失败可见，且夹具写计数不变；持久审计正常写路径另由Go测试覆盖。|
|M22|数字回环地址、默认只读、明确的单元／地址／数量／类型写入范围确认、启动和按 run_id 停止；`Tools.controller`|Android实际本机127.0.0.1控制器只读拒绝、取消开放写入、确认窄范围、错误单元/地址/Origin拒绝、回读及停止通过；不监听公网。|
|M23|四个可发现标签页和具名操作；中文帮助；公共文件／集合页负责 next_config 和返回初始集合|四个标签与高级工具真实Android可达、中文帮助已实现；集合轮转证据见C4，不等于所有物理文件选择场景均已测试。|

### OPC UA（10项）

|编号|实际Flutter入口|已有证据与未逐项实测的边界|
|---|---|---|
|U1|`端点` → `发现端点` → 端点卡片 → `连接与安全草稿`；策略、模式、anonymous/username/certificate、独立核对的指纹/CA、客户端/身份凭据证书和私钥字段；None/旧策略单独勾选。`_endpoints`、`_connection`|Android实际端点发现→None显式勾选→选择只改草稿零I/O→成功浏览通过；未知模式拒绝与不自动信任指纹由widget回归覆盖。真实证书/认证组合未全面设备验证。|
|U2|`浏览`/`引用`；节点卡片选择、后退/前进、NodeId 跳转；`引用筛选`提供方向、引用类型、子类型、数量。`_referenceCards`、`_referenceFilter`、`_jump`|Android实际浏览、缓存后退/前进零I/O、宽屏工作台通过；引用筛选作用域由模型/控件覆盖。|
|U3|`属性` → `刷新全部属性`；独立属性状态、类型、值、源/服务器时间；`读取此属性`和完整详情。`_attributes`|Android实际属性列表与Value/ValueRank、写后再次读取99通过；未提供的服务器时间可能显示零时间，属于显示边界而非伪造设备时间。|
|U4|`类型化写入` → 属性选择 → 标量/一维数组、LocalizedText、QualifiedName、NodeId、GUID、DateTime、ByteString 等专用控件 → 确切目标和值预览 → 确认。`_write`、`UaTypedEditor`|Android实际99预览取消零I/O、Int32越界零I/O、改回99重新预览后确认写入恰好一次并回读99通过。其他精确标量/结构/数组由widget与Go回归覆盖。|
|U5|方法节点卡片 → `只读取方法签名` → 按签名顺序生成类型化参数 → 确切对象/方法/参数确认 → 输出与状态。`_method`、`_methodCall`|Android实际方法签名读取不调用、越界参数拒绝、取消不调用、确认Double(6)=12且调用计数+1通过。|
|U6|`路径与操作` → 读取唯一浏览路径、复制缓存路径、按带命名空间/转义的路径解析；显式复制 NodeId。`_actions`、`_path`、`_copy`|路径解析/复制NodeId的专用控件与共享内核已实现，格式/精度/缓存语义有模型回归；本轮未执行Flutter实际路径往返，不用Go路径测试冒充整机证据。|
|U7|`订阅` → 添加当前节点，编辑间隔/次数/总时限/只读重连 → 预览确认；每项最新值、状态、时间、完整通知、按 ID 停止或停止全部。`_subscribe`、`_subscriptions`|Android实际Temperature/Pressure双订阅收到值，同时读取属性，按ID停止一个保留另一个，再后台停止并不自动重放通过。|
|U8|`端点` → `连接历史` → 选择并检查连接草稿；清除需确认。`_connections`|成功连接历史/选择草稿/清除确认已实现，本地打开选择不联网的widget测试通过；本轮未单独完整点击成功连接历史闭环。|
|U9|`生成客户端身份` → 新证书/私钥路径和应用 URI → 目标确认；运行可取消；端点页展示生成路径、应用 URI、证书 DER SHA-256 指纹。`_identity`|身份生成/新文件独占/DER SHA256指纹入口已实现，取消零调用与Go证书回归通过；本轮未单独在Flutter界面确认生成实际身份文件。|
|U10|小屏单面板/宽屏双面板；短屏与大字号外层滚动；独立日志；读操作取消；后台关闭确认、停止任务/订阅，恢复不自动运行。`_header`、`_body`、`_logs`、生命周期观察者|Android实际surface宽度变化及Flutter生命周期回调下停止/恢复不重放通过；这不等同物理系统旋转/切后台或所有设备字体。2倍字号/短屏组件检查通过。|

## 未由本次结果证明的范围

物理ARM64手机、USB串口型号/授权/拔插、外部工业设备、真实TLS/mTLS基础设施、所有输入法/字体、iOS未执行验收。表中少数高级子流程已实现且有组件或Go证据，但没有独立Android逐步骤测试，已逐项列出。API29模拟器中的个别emoji字形和OPC缺省零时间显示是已知展示边界；不能据请求UTF-8字节正确宣称所有设备字形一致。

这份报告记录冻结版本的实际证据，不保证与五个上游工具的CLI格式、布局和所有插件逐字节相同。移动端采用专用Flutter触控界面；桌面原有TUI保留。没有访问真实工业目标，没有自动重放写入，也没有更改系统安全策略。
