# Flutter 通用、HTTP、Kafka、MQTT 功能对应表

核对时间：2026-10-01。移动业务界面位于 `mobile/lib`；Android Java 只提供 MethodChannel/JNI、SAF 与 USB 平台服务。桌面 TUI 保留。

证据分级：`已执行` 指本机真实 Flutter analyzer/model/widget 测试；`待 Android` 指已写真实 MethodChannel→JNI→Go→localhost/host fixture 整机测试，尚不能用本机 widget 结果替代 Android 运行结果。首候选 `19d4b2e948973d64e2014a1570cd26a092e3ef8c` 与本表之后的本地补丁必须分别记录，不能交叉声称通过。

主要实现缩写：

- Shell：`mobile/lib/app/app.dart`；Session：`core/session.dart`
- Form：`features/requests/request_form.dart`；Files：`features/files/file_tools.dart`
- Crypto：`features/http/crypto_editor.dart`；HTTP：`features/http/http_tools.dart`
- History：`features/history/history_page.dart`；Results：`features/messaging/messaging.dart`
- Kafka：`features/messaging/kafka_entities.dart`；MQTT model：`features/messaging/messaging_models.dart`
- Review：`shared/review_dialog.dart`；SQL grid：`shared/widgets.dart`

## 通用

| ID | 实际 Flutter 入口与行为 | 服务/状态与证据 |
|---|---|---|
| C1 | 请求卡片搜索、五协议 chips、环境 dropdown、新建/复制/删除菜单；表单选择协议/所有动作；只有执行按钮发起请求 | Shell/Form/Session。已执行 `app_widget_test`：三底栏、导航不执行、编辑值保留；全部动作来自完整 Go catalog。待 Android 五协议整机 |
| C2 | 独立表单与整集合 YAML、底部固定保存/执行、错误 SnackBar；重载需明确确认；表单保存拒绝丢弃未保存 YAML；所有草稿仅内存，无 restorationId | Session 的按集合草稿、App 原生 Flutter TextField。已执行 `session_test`、`collection_test`、`app_widget_test`；待 Android UTF-8/emoji/生命周期/原文件不变用例 |
| C3 | 设置→导入配置/转换；ZIP；附件；私有文件列表；系统新文档导出；1–8GiB 限额选择与停止传输；HTTP multipart 二进制文件选择 | Files 通过平台流式接口；配置导入先转换预览再放入 YAML 草稿。已执行 `files_workflow_test`：files.read 字符串、取消清理请求、4MiB multipart 引用；真实 SAF/ZIP/大文件平台测试由 `MobileFilesTest` 验证，待 Android |
| C4 | 设置→私有文件预览切换、next_config 下一集合、返回初始集合；启动固定根后恢复保存的相对集合；切换保留各集合独立内存草稿 | Go `config.switch` token；Session `switchCollection`；只保存路径偏好。已执行 `collection_test`：同 ID 隔离、无写/网络、YAML 恢复。待 Android 嵌套文件/SAF |
| C5 | 工作台结果/执行日志、明确取消、复制/导出/重新执行；可取回完整大结果并导出；原始与转换后响应分别标记；前台事件与订阅事件独立路由 | Session 单一 Poll、run_id/epoch、最多500事件、结果页30张分页、视觉通知节流；result.get 淘汰报错。已执行 pause/dispose/迟到 run、Modbus 来源测试与已完成结果截图；待 Android 慢响应取消 |
| C6 | 设置→中文操作帮助、项目许可、Flutter 依赖许可、可选择完整 Go build SHA、USB 硬件验收说明；深/浅/系统外观 | `settings.get/save` 严格非秘密字段；`help.read/licenses.read` 固定资产。已执行桥接合同与三尺寸 Flutter 渲染；真实系统字体/IME/系统栏待 Android |

## HTTP、模板、加密、历史

| ID | 实际 Flutter 入口与行为 | 服务/状态与证据 |
|---|---|---|
| H1 | Method/URL/timeout；headers/query 原生键值编辑；Bearer/Basic 密码遮罩；高级 CA/cert/key 文件选择、跳转/TLS 参数 | Form＋共享 RunCollection；Review 单独显示目标/正文/影响范围与脱敏 headers。已执行 payload变化/敏感头测试；256KiB UTF-8剪贴板上限拒绝截断并提示导出；真实 GET/POST 与公用测试密钥待 Android |
| H2 | 文本正文保留空白；JSON 精确数值编辑；URL 表单键值；multipart 文本与二进制选择，不靠原始命令JSON运行 | Form/Files，file() 引用由实际平台文件路径产生。已执行 multipart、UInt64、空白不变；二进制 wire/SAF 待 Android |
| H3 | body_file/body_stream、max_upload_bytes、response_file/max_response_bytes；文件 picker 与可取消流式传输；response-file 显示字节数/SHA256并直接导出 | Form/Files/Results，上传下载约束仍由 Go 执行。平台大文件/清理测试待 Android；不宣称已在物理设备验证8GiB |
| H4 | 命名编码器列表；算法 picker；AES key/IV与各自编码、ciphertext base64/base64url；Base64 空白/缺 padding 开关；none 不带AES字段；单独实验室 | Crypto/HTTP。已执行算法字段模型；真实 Flutter AES-128-CBC 控件→加密请求/解密回包整机测试采用独立 OpenSSL 固定公开向量，待 Android |
| H5 | 请求有序转换：添加/编辑/删除/上移；encode/decode/encrypt/decrypt、body/header/query/json；响应 body/fields、JSONPath列表、skip条件、parse_json、utf8/utf8-sig | Crypto `TransformEditor` 与实际参数数组，保留原顺序。已执行模型/编译；真实请求字节与响应解密整机用例待 Android |
| H6 | 现有模板保留原生文本；执行时共享内核解析 file/response/链依赖；首次预览不读取网络依赖；运行时每个写入/动态目标仍需确认 | Session/全局 interaction 队列。界面不自行替换模板、不把预览当运行。依赖/TLS真实链证据待 Android，不把普通 GET 当链覆盖 |
| H7 | 全局确认、遮罩 prompt、原始类型标签 select；select 使用原始 selection_index；取消明确 false；后台清空待办并关闭执行确认 | Shell interaction 队列/Go nonce。已执行 `interaction_test`：数值/布尔/对象选择、敏感输入、取消零 run；生命周期测试已执行 |
| H8 | 工具→单次变量/请求覆盖；fields/header/query/form多行键值，重复 query保留；URL/body/Bearer/Basic单次覆盖；不保存到原请求，重新执行保留本次覆盖 | HTTP `temporaryOverrides`＋preview.overrides。已执行重复/删除/格式/互斥模型；复杂链变量实际路径待 Android |
| H9 | 工具→生成 cURL；另一个明确“执行依赖后生成 cURL”确认；敏感输出警示、复制/导出，不自动执行 shell | HTTP→http.curl，异步依赖结果进入统一事件。API行为保留；Flutter交互待 Android |
| H10 | 工具→jq 筛选表达式；使用 http.filter.start；任务进入共享运行/取消状态，结果为 query | HTTP/Results。数据从当前解析/文本正文转换为正确 JSON 字符串；Go限定执行时间，用户可取消。待 Android jq取消用例 |
| H11 | HTTP结果卡正文/响应头/原始分段；转换后响应单独命名；复制/完整缓存检索/长文本分页搜索/新文档导出/重新执行 | Results。已执行完成状态与截图；不会对历史文本再做Base64解码，原始二进制单列。Android实际字体/大响应待验证 |
| H12 | 设置中明确 opt-in HTTP历史；历史搜索、时间created_at、状态/大小、正文/转换正文/原始Base64、单项删除二次确认；只读禁止删除 | History/Session。已执行历史文本'test'/中文模型；HTTP整机覆盖literal/中文/二进制与时间，待 Android |
| H13 | 历史→SQL；多结果集表格、50行分页、本地筛选；原查询可用.tables/.schema；collection元数据列表 | History/SQL grid，history.query。已执行组件编译；整机 SELECT 中文/文本/数字表格待 Android |
| H14 | 历史→集合重命名/合并/删除/迁移与SQL变更预览；显示真实SQL/备份字节数；明确选择新备份名再执行token；只读禁用变更 | History→history.collection.preview/history.preview/history.execute。Go负责数据库身份、事务、备份与无覆盖；Flutter取消/迁移实际证据待 Android |

## Kafka、Schema Registry、Connect

| ID | 实际 Flutter 入口与行为 | 服务/状态与证据 |
|---|---|---|
| K1 | Kafka表单 broker/timeout；TLS/mTLS、PLAIN/SCRAM、独立Registry URL/用户/密码/Bearer/证书 | Form，敏感输入遮罩、预览脱敏。控件不建连接；五协议真实整机部分待 Android |
| K2 | topics/brokers/offsets/topic-config真实结果→“浏览实体/分区/管理操作”；搜索/分页、分区Leader/副本/ISR、分区消费草稿、配置/偏移草稿 | Kafka支持kadm keyed map和大小写字段，不把整个map当topic名。已执行实体模型；真实broker消费与界面待 Android |
| K3 | 实体详情→准备更改配置/扩充分区/删除；新建请求动作create-topic有topic/分区/副本/键值configs；底部明确预览写入 | Form/Kafka/Review。所有实际写仅由run+token；局部控件与cancel已执行；各管理wire需 Android/Go对应证据 |
| K4 | groups/group/lag实体列表→成员详情/单组Lag/删除；准备Lag显式groups:[选中组]；返回只浏览缓存 | Kafka/Go；已执行group草稿不扩大范围模型；Go singular group兼容由独立wire回归证明，Android组详情待执行 |
| K5 | consume的topic/partition列表/offset/start_time/limit；键值contains/prefix筛选；原生表单可用earliest/latest/most-recent；显式Stop | Form＋共享引擎，无本地伪消费。真实fixture earliest有限消费整机已写，待 Android |
| K6 | 独立记录浏览：本地搜索、精确offset/时间/分区排序、分页；详情键/值、headers、null/tombstone、原始base64、上一/下一缓存记录、复制导出、从偏移准备草稿 | Results `KafkaBrowser`。模型精确数值已执行；fixture binary key /wA= 与两条记录整机待 Android |
| K7 | produce原生key/value、headers/partition、text/json/avro picker、独立subject/version字段；写预览不隐式注册Schema | Form/Review共享engine；typed/crypto无关地保留原payload；实际Avro wire由Go原有测试，Flutter整机produce覆盖仍需按最终CI记录 |
| K8 | Schema列表→版本→定义；原生Avro Schema输入；准备注册、版本删除、subject软删、永久清除需完整subject文本；共享写预览 | Kafka/Form。已执行HTTP body→实体模型；新增真实Schema注册/取消/软删/purge计数整机待 Android |
| K9 | Connector列表→状态/tasks/config；原生键值编辑配置；准备更新/暂停/恢复/删除；动作准备会清理旧body/config残留 | Kafka/Form。已执行准备动作字段隔离；新增真实更新取消/确认、pause/resume计数整机待 Android |

## MQTT

| ID | 实际 Flutter 入口与行为 | 服务/状态与证据 |
|---|---|---|
| Q1 | Broker/clientId/认证/TLS、topic或topics列表、QoS、limit/ignore_retained/timeout/reconnect字段；subscribe/read-one明确执行 | Form/共享引擎。实际本地broker精确主题read-one整机待 Android |
| Q2 | Publish文本/HEX/Base64、QoS/retain、精确topic与正文可读预览；取消无写 | Form/Review。二进制参数保留；实际broker发布/retained生命周期需最终Android证据 |
| Q3 | 原生主题树展开/折叠、搜索、精确topic历史；保留/a、a//b、a/、a的独立层级与数据 | MQTT model/Results。已执行空层级/精确删除；真实/sensors//temp/整机待 Android |
| Q4 | 消息详情文本/HEX/Base64/JSON/MessagePack；按主题有界100条、200主题/1000条/8MiB总预算；可删除本地条目、清空、导出、跟随/冻结 | TopicCache，淘汰计数可见，作用域绑定run，缓存操作不改变broker订阅。已执行预算与精确topic隔离；真实消息界面待 Android |
| Q5 | 字段选择保留无效旧selector；布尔/数值/数字前缀文本/数组长度/MessagePack map项数、字节索引/长度/到达速率；缩放图、min/max、冻结/清空 | numericFields/point/SeriesPainter；不歧义bracket路径、过滤retained和非有限值。已执行路径/前缀/布尔/数组/map/速率模型；真实连续图表待 Android |
| Q6 | preview-retained结果显示有限扫描全部主题分页/搜索/数量/警示；“准备清理”携带原confirm_topics/confirm_token进入clean-retained草稿，之后仍需明确写确认 | Results/Review，共享Go负责快照校验/部分完成/取消。不得从通配符直接授权清理；真实broker清理整机待 Android |
| Q7 | 订阅/重连事件状态与错误可读；取消始终可见；关闭详情只停止本地刷新；后台取消、回来不重连/重放 | Session/Results。已执行生命周期迟到事件/迟到run/取消语义；真正broker中断重连需Android/Go对应证据 |

## 当前证据范围

- 第一批：独立候选复制目录与 manifest 相同；本机54测试、analyzer零问题。其 Android Actions 结果单独由发布记录记录。
- 第二批冻结（2026-10-01 07:00 UTC）：完整 `flutter test` 71/71 通过；完整 `flutter analyze` 零问题。包含文件/集合/提示/确认/消息模型、精确JSON纠错、retained完整快照、生命周期竞态和剪贴板UTF-8边界回归。日志由独立候选记录保存；Android整机状态仍需单列。
- 截图 `mobile/build/flutter-evidence/flutter-{phone,narrow,landscape,http-form,write-review,http-completed-result,file-limit}.png` 是真实Flutter widget渲染、合成数据；不等于Android emulator、真实协议或物理USB证据。
- Android整机入口：`integration_test/app_test.dart`，导入 Kafka 管理、OPC、Modbus 工作流；使用公开测试密钥、localhost与明确adb-reversed回环夹具。不能以Go引擎或widget通过代替此结果。
