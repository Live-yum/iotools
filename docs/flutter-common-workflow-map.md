# Flutter 通用、HTTP、Kafka、MQTT 功能对应表

证据冻结：2026-10-01 16:56 UTC。可执行源码版本 `103853150b0d2636e5609e9b4b473174e3bceb42`。

[Android 完整验收运行](https://github.com/Live-yum/iotools/actions/runs/36889678773)成功；同SHA桌面Windows x64、Linux x64、Linux ARM64原生CI成功。Android API29 x86_64软件模拟器：117项Flutter模型/组件测试、18项Android平台测试、5项主业务整机测试、1项独立OPC整机测试、1项普通AOT验收测试全部通过；tearDownAll不算业务测试。普通AOT验收内含六个协议场景和启动恢复。来源为实际日志、Gradle XML、AOT JSON与PNG，非仅存在测试源码。

“已实现”与“已在Android逐路径执行”分别记录。下表列出专用Flutter入口及可核对证据，没有把一个绿色job等同全部硬件/参数组合验证。物理ARM64、USB设备、真实工业设备、真实证书基础设施和iOS尚未验收；桌面轻量TUI保留。

| 编号/功能 | 实际Flutter入口与实现 | 最终证据与明确边界 |
|---|---|---|
| C1 | 请求卡片搜索、五协议 chips、环境 dropdown、新建/复制/删除菜单；表单选择协议/所有动作；只有执行按钮发起请求 | AOT 普通入口已实际执行五协议；Flutter 主套件验证卡片、表单与显式执行。搜索/复制等控件另有组件测试，不把全部动作目录当作已逐个联网验收。 |
| C2 | 独立表单与整集合 YAML、底部固定保存/执行、错误 SnackBar；重载需明确确认；表单保存拒绝丢弃未保存 YAML；所有草稿仅内存，无 restorationId | Android HTTP 整机验证表单、AES参数和内存YAML秘密草稿在生命周期回调后保留、未写回文件；本地精确JSON/集合草稿/生命周期回归通过。真实中文请求字节已验证；所有设备字体与IME组合未逐一验证。 |
| C3 | 设置→导入配置/转换；ZIP；附件；私有文件列表；系统新文档导出；1–8GiB 限额选择与停止传输；HTTP multipart 二进制文件选择 | 18项Android平台测试包含真实测试DocumentProvider的流式导入、ZIP、取消/清理与路径保护；Flutter文件控件测试通过。平台测试不是用户亲自操作系统选择器的全部流程，也未传输物理8GiB文件。 |
| C4 | 设置→私有文件预览切换、next_config 下一集合、返回初始集合；启动固定根后恢复保存的相对集合；切换保留各集合独立内存草稿 | 普通AOT实际损坏配置恢复：取消保持原件，选择合法集合后启动，损坏文件未重置；嵌套根/兄弟集合切换由Go及Flutter回归证明。next_config全程未单独在Android点击验收。 |
| C5 | 工作台结果/执行日志、明确取消、复制/导出/重新执行；可取回完整大结果并导出；原始与转换后响应分别标记；前台事件与订阅事件独立路由 | HTTP写入取消为零、确认恰好一次；OPC双订阅按ID停止、后台不重放均实际通过。run_id迟到事件、大结果淘汰/分页由组件与内核回归覆盖；大响应全部导出未逐一整机执行。 |
| C6 | 设置→中文操作帮助、项目许可、Flutter 依赖许可、可选择完整 Go build SHA、USB 硬件验收说明；深/浅/系统外观 | 真实Android页面与图标截图可读、三底栏一致；117项本地组件/模型测试及analyzer通过。帮助、版本和许可入口已实现；物理设备无障碍/字体组合未全面验收。 |
| H1 | Method/URL/timeout；headers/query 原生键值编辑；Bearer/Basic 密码遮罩；高级 CA/cert/key 文件选择、跳转/TLS 参数 | Flutter真实AES POST与AOT GET/POST通过，取消零写、确认一次，渲染目标/正文可审查；TLS/mTLS/代理等高级参数由共享内核测试及表单覆盖，未在Android逐组合实测。 |
| H2 | 文本正文保留空白；JSON 精确数值编辑；URL 表单键值；multipart 文本与二进制选择，不靠原始命令JSON运行 | 真实AES请求的完整UTF-8字节对照独立固定向量通过；UInt64/空白/multipart文件引用由组件测试覆盖。完整multipart二进制上传未在本轮Android单独执行。 |
| H3 | body_file/body_stream、max_upload_bytes、response_file/max_response_bytes；文件 picker 与可取消流式传输；response-file 显示字节数/SHA256并直接导出 | 流式文件/未知长度/取消/越界保护通过Android平台测试；上传下载边界由Go测试覆盖。物理8GiB文件和所有HTTP流式导出组合未整机验证。 |
| H4 | 命名编码器列表；算法 picker；AES key/IV与各自编码、ciphertext base64/base64url；Base64 空白/缺 padding 开关；none 不带AES字段；单独实验室 | Android真实AES-128-CBC编辑→请求加密→响应解密通过独立公开向量；AES其他密钥长度、ECB、Base64URL等由Go向量与控件回归证明，不能称Android所有算法组合均已跑过。 |
| H5 | 请求有序转换：添加/编辑/删除/上移；encode/decode/encrypt/decrypt、body/header/query/json；响应 body/fields、JSONPath列表、skip条件、parse_json、utf8/utf8-sig | 请求encrypt和响应decrypt真实往返通过；有序编辑、JSONPath/条件/编码语义由组件与共享内核测试覆盖。所有转换组合未逐个整机运行。 |
| H6 | 现有模板保留原生文本；执行时共享内核解析 file/response/链依赖；首次预览不读取网络依赖；运行时每个写入/动态目标仍需确认 | 模板/依赖链/文件引用及逐步写入确认入口已实现，共享内核回归通过；复杂链、交互与TLS例外组合未单独Android整机验证。 |
| H7 | 全局确认、遮罩 prompt、原始类型标签 select；select 使用原始 selection_index；取消明确 false；后台清空待办并关闭执行确认 | 原始类型选择、遮罩prompt、取消零运行与后台关闭确认的Flutter组件回归通过；普通写确认已实际Android验证，全部模板prompt/select网络链未逐个执行。 |
| H8 | 工具→单次变量/请求覆盖；fields/header/query/form多行键值，重复 query保留；URL/body/Bearer/Basic单次覆盖；不保存到原请求，重新执行保留本次覆盖 | 临时覆盖的重复键/删除/互斥/不保存语义经模型与内核测试；Android首页已验证profile变量解析，复杂依赖链临时覆盖未单独整机执行。 |
| H9 | 工具→生成 cURL；另一个明确“执行依赖后生成 cURL”确认；敏感输出警示、复制/导出，不自动执行 shell | cURL与执行依赖后生成的独立确认入口已实现、编译通过，共享内核有回归；未单独执行Android cURL链交互，不自动运行输出shell。 |
| H10 | 工具→jq 筛选表达式；使用 http.filter.start；任务进入共享运行/取消状态，结果为 query | jq异步任务/取消入口已接共享内核，执行时间有界；组件/内核检查通过，未单独执行Android jq取消用例。 |
| H11 | HTTP结果卡正文/响应头/原始分段；转换后响应单独命名；复制/完整缓存检索/长文本分页搜索/新文档导出/重新执行 | 真实HTTP响应、解密正文与状态截图通过；完整结果缓存、分页和导出有组件/内核回归。API29个别emoji字形显示仍存在视觉差异，不能将请求字节正确等同所有字体显示正确。 |
| H12 | 设置中明确 opt-in HTTP历史；历史搜索、时间created_at、状态/大小、正文/转换正文/原始Base64、单项删除二次确认；只读禁止删除 | Android实际开启历史、保存AES及普通请求，literal详情显示test与created_at通过；中文/二进制响应请求也运行。中文/二进制每种历史详情控件未逐个单独断言；原始数据由Go/组件回归覆盖。 |
| H13 | 历史→SQL；多结果集表格、50行分页、本地筛选；原查询可用.tables/.schema；collection元数据列表 | Android实际执行含数字、test、中文的SELECT并显示DataTable，截图可核对；.tables/.schema、多语句、分页与过滤由共享内核/组件覆盖，未逐项设备操作。 |
| H14 | 历史→集合重命名/合并/删除/迁移与SQL变更预览；显示真实SQL/备份字节数；明确选择新备份名再执行token；只读禁用变更 | 集合管理与事务预览/备份/只读控件已实现；Go实际迁移、WAL备份、回滚、过期token与双驱动兼容回归通过。Android整机本轮只执行只读SELECT，未实际删除/迁移用户数据库。 |
| K1 | Kafka表单 broker/timeout；TLS/mTLS、PLAIN/SCRAM、独立Registry URL/用户/密码/Bearer/证书 | 实际普通本地Kafka连接/消费通过；TLS/SASL/SCRAM与Registry认证参数已实现并有共享引擎测试，Android未逐一验证认证组合。 |
| K2 | topics/brokers/offsets/topic-config真实结果→“浏览实体/分区/管理操作”；搜索/分页、分区Leader/副本/ISR、分区消费草稿、配置/偏移草稿 | 真实Kafka记录和Schema/Connector实体浏览已在Android通过；topic/broker/partition实体模型与草稿生成有组件回归，全部管理实体未逐类整机验证。 |
| K3 | 实体详情→准备更改配置/扩充分区/删除；新建请求动作create-topic有topic/分区/副本/键值configs；底部明确预览写入 | 创建/扩分区/配置/删除的原生表单与写确认已实现；内核本地broker及控件保护测试通过。Android本轮不含全部topic管理确认写入。 |
| K4 | groups/group/lag实体列表→成员详情/单组Lag/删除；准备Lag显式groups:[选中组]；返回只浏览缓存 | 组/成员/lag列表与单组作用域草稿已实现，模型及Go组查询回归通过；Android组详情与删除未单独整机执行。 |
| K5 | consume的topic/partition列表/offset/start_time/limit；键值contains/prefix筛选；原生表单可用earliest/latest/most-recent；显式Stop | 真实earliest有限消费与普通AOT消费通过；offset/time/partition筛选由内核测试覆盖，所有起点组合未逐项Android验证。 |
| K6 | 独立记录浏览：本地搜索、精确offset/时间/分区排序、分页；详情键/值、headers、null/tombstone、原始base64、上一/下一缓存记录、复制导出、从偏移准备草稿 | Android真实两条记录浏览、原始binary key /wA=、搜索/缓存前后记录入口通过；精确数值及截断信封/完整结果处理由模型回归覆盖，超大记录淘汰未单独设备验证。 |
| K7 | produce原生key/value、headers/partition、text/json/avro picker、独立subject/version字段；写预览不隐式注册Schema | produce/headers/partition/Avro原生表单与明确写确认已实现，实际Avro线协议由Go测试证明；本轮Flutter设备测试不含produce成功路径，不能用消费成功替代。 |
| K8 | Schema列表→版本→定义；原生Avro Schema输入；准备注册、版本删除、subject软删、永久清除需完整subject文本；共享写预览 | Android真实注册取消零修改、确认注册+1，版本实体浏览、subject软删+1及明确subject永久清理+1通过合成Registry计数验证。 |
| K9 | Connector列表→状态/tasks/config；原生键值编辑配置；准备更新/暂停/恢复/删除；动作准备会清理旧body/config残留 | Android真实Connector配置编辑、取消零修改、确认更新+1、暂停+1、恢复+1及实体详情通过；删除入口已实现但本轮未执行删除成功。 |
| Q1 | Broker/clientId/认证/TLS、topic或topics列表、QoS、limit/ignore_retained/timeout/reconnect字段；subscribe/read-one明确执行 | Android与普通AOT实际读取本地broker消息通过；QoS/TLS/认证/重连参数由表单和内核覆盖，未逐一设备组合实测。 |
| Q2 | Publish文本/HEX/Base64、QoS/retain、精确topic与正文可读预览；取消无写 | 文本/HEX/Base64/QoS/retain发布和写确认已实现；共享broker回归覆盖发布语义。本轮Flutter设备用例为read-one，未单独执行publish成功。 |
| Q3 | 原生主题树展开/折叠、搜索、精确topic历史；保留/a、a//b、a/、a的独立层级与数据 | Android真实/sensors//temp/原始主题与历史视图通过；/a、a//b、a/、a分离和树操作由组件模型回归证明，不归一化或扩大订阅范围。 |
| Q4 | 消息详情文本/HEX/Base64/JSON/MessagePack；按主题有界100条、200主题/1000条/8MiB总预算；可删除本地条目、清空、导出、跟随/冻结 | Android真实消息详情/缓存入口通过；历史预算、精确删除、冻结与淘汰由组件回归覆盖。所有MessagePack/导出动作未逐一设备执行。 |
| Q5 | 字段选择保留无效旧selector；布尔/数值/数字前缀文本/数组长度/MessagePack map项数、字节索引/长度/到达速率；缩放图、min/max、冻结/清空 | 路径转义、数值前缀、布尔、数组/MessagePack map计数、速率等模型回归通过，原生图表已实现；连续实际broker趋势交互未单独整机执行。 |
| Q6 | preview-retained结果显示有限扫描全部主题分页/搜索/数量/警示；“准备清理”携带原confirm_topics/confirm_token进入clean-retained草稿，之后仍需明确写确认 | 完整retained快照预览、确认token与清理草稿入口已实现，内核本地broker及组件语义回归通过；Android本轮未实际执行retained清理。 |
| Q7 | 订阅/重连事件状态与错误可读；取消始终可见；关闭详情只停止本地刷新；后台取消、回来不重连/重放 | Flutter生命周期/迟到事件与取消回归通过，订阅状态入口已实现；Android真实broker断线重连未单独执行，不能用OPC后台用例代替。 |

实现路径：`mobile/lib/app/app.dart`、`core/session.dart`、`features/requests`、`features/files`、`features/http`、`features/history`、`features/messaging`、`shared/review_dialog.dart`。测试入口：`mobile/integration_test/app_test.dart`、`kafka_management_test.dart`及对应`mobile/test`。

完整分组报告见`docs/flutter-acceptance-103853.zh-CN.md`。本次更新仅为验收文档，不修改103853可执行源码。
