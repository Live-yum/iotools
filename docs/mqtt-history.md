# MQTT 主题历史、图表与 MessagePack

本轮逐项核对 [mqttui 固定版本 afdd404f](https://github.com/EdJoPaTo/mqttui/tree/afdd404f1d57d9ea5065d4853560173d5573e808)
的真实代码，独立实现功能。没有复制 GPL 应用代码，也没有新增外部运行时或编解码依赖。

## 主题树和历史

在主题树选中收到过消息的主题后：

- `h`：打开该主题历史表；显示接收时间UTC、QoS、retain、原始字节数、格式和选中值
- `g`：打开时间图表；图表和历史可以在弹层中用 `g`/`h` 切换
- `s`：选择数值字段，例如 `$.temperature`、`$.readings[0]`、`$['key.with.dot']`
- 字段选择只支持有界的对象键/数组下标链，与上游点选嵌套字段相对应；不执行递归查询、通配扫描或过滤表达式
- 也可选择二进制字节下标0..4095；纯二进制主题默认看第0字节
- 历史跟随最新消息；上下/翻页/Home/End浏览时保留所选旧行，`f` 恢复跟随
- Enter查看所选历史消息和可用HEX前缀；Ctrl-Y使用本程序统一复制功能
- Delete/Backspace只移除选中的较早本机历史项；最新消息不允许移除，不发送任何broker消息
- Esc仅关闭当前弹层，不停止订阅；F8在历史、详情、字段选择和主题搜索中均可取消订阅
- `/` 打开大小写不敏感的主题搜索，Enter选择结果并展开匹配路径；`o` 展开全部，`O` 收起全部。搜索不会改动左侧请求集合搜索

历史面板包含可用窗口中的消息速率，计算时排除retained回放。数字图表同样排除
retained、缺失字段、非有限值及无法保留完整结构的截断消息，至少需要两个有效点。
时间轴是客户端接收时间，不伪装为设备采样时间。布尔值按0/1、数字开头文本按首个
词、数组按长度解释；MessagePack map可按项数解释。64位整数在历史值中保持精确；
图表使用float64近似。缺失字段明确跳过，不以根对象替代。

## 确定的内存边界

- 每主题最多128条，最多256个主题、2048条历史项
- 缓存载荷及主题名按总计8MiB预算淘汰最旧项，面板标出淘汰计数
- 每项文本预览最多8KiB，完整结构预览最多16KiB/32层，原始二进制前缀最多4KiB
- 截断会明确标记，完整原始事件可使用CLI采集；上限不隐式扩大broker订阅范围
- 切换或重新运行请求会清空本次历史，不跨设备/账号混合；历史不写磁盘

这些有界行为与上游默认内存历史不同，是本程序明确的资源限制。

## MessagePack 检测和表示

按上游实际检测顺序：有效UTF-8先尝试JSON，再作为文本；只有非UTF-8载荷才尝试
MessagePack。成功时事件增加 `payload_format: messagepack` 与
`payload_messagepack`；原始HEX/Base64仍完整保留。普通二进制或不支持的结构继续
显示为binary，不伪装成UTF-8文本。

独立解码器支持fixint及全部有符号/无符号整数宽度、float32/64、null、bool、
所有字符串/二进制长度形式、数组、map与extension。整数以JSON number保留精度。
二进制、无效UTF-8字符串和扩展使用显式带 `messagepack_type` 的对象保留类型及
Base64；扩展同时保留 `extension_type`。包括type=-1时间戳扩展在内，不擅自转换或
丢弃扩展字节。NaN/Infinity使用显式类型表示，不能进入数值图表。

安全限制：整个载荷必须恰好消费完，最多4MiB、32层、16384个节点；长度字段在
分配前检查。map允许字符串、数字、bool、null键，按显示名重复时拒绝解码；复合
数组/map/二进制/扩展键保持原始binary，因为把嵌套复合键转为JSON名称可能指数膨胀。
上游较宽松的尾随数据启发式和任意复合map键不照搬。

## 源码核对矩阵

| 上游实际功能 | 本实现 | 核对来源 |
|---|---|---|
| 每主题历史，选择旧消息 | 有界历史表、详情和跟随控制 | [mqtt_history.rs](https://github.com/EdJoPaTo/mqttui/blob/afdd404f1d57d9ea5065d4853560173d5573e808/src/interactive/mqtt_history.rs)、[table.rs](https://github.com/EdJoPaTo/mqttui/blob/afdd404f1d57d9ea5065d4853560173d5573e808/src/interactive/details/table.rs) |
| 本机旧历史删除，保留最新 | Delete/Backspace实现，完全不发布 | [interactive/mod.rs](https://github.com/EdJoPaTo/mqttui/blob/afdd404f1d57d9ea5065d4853560173d5573e808/src/interactive/mod.rs) |
| 数值/字段/字节图表 | 时间折线、字段选择、非有限/retained过滤 | [graph/point.rs](https://github.com/EdJoPaTo/mqttui/blob/afdd404f1d57d9ea5065d4853560173d5573e808/src/interactive/details/graph/point.rs)、[graph/mod.rs](https://github.com/EdJoPaTo/mqttui/blob/afdd404f1d57d9ea5065d4853560173d5573e808/src/interactive/details/graph/mod.rs) |
| MessagePack自动检测和树值 | 独立有界解码，原始字节不丢失 | [payload/mod.rs](https://github.com/EdJoPaTo/mqttui/blob/afdd404f1d57d9ea5065d4853560173d5573e808/src/payload/mod.rs)、[messagepack/mod.rs](https://github.com/EdJoPaTo/mqttui/blob/afdd404f1d57d9ea5065d4853560173d5573e808/src/payload/messagepack/mod.rs) |
| 主题搜索及展开/收起 | `/`、`o`、`O` | [interactive/mod.rs](https://github.com/EdJoPaTo/mqttui/blob/afdd404f1d57d9ea5065d4853560173d5573e808/src/interactive/mod.rs) |
| 任意消息发布 | 上游是独立CLI子命令；本程序已有publish请求 | [main.rs](https://github.com/EdJoPaTo/mqttui/blob/afdd404f1d57d9ea5065d4853560173d5573e808/src/main.rs) |
| 订阅中任意交互发布 | 此固定版本上游没有该功能，不作为缺口补造 | 同上及interactive/mod.rs键盘处理 |
| 全屏布局 | 上游本身为全屏分栏，没有独立全屏切换键；本程序使用完整终端弹层中的历史/图表切换 | [details/mod.rs](https://github.com/EdJoPaTo/mqttui/blob/afdd404f1d57d9ea5065d4853560173d5573e808/src/interactive/details/mod.rs)、[ui.rs](https://github.com/EdJoPaTo/mqttui/blob/afdd404f1d57d9ea5065d4853560173d5573e808/src/interactive/ui.rs) |

上游图表与历史/载荷可同时分栏显示，本程序使用弹层切换；字段使用JSONPath语法
选择，不复制原版JSON树选择控件。版式和快捷键不同，不宣称像素级复刻。
保留主题清理仍使用已有的预览、精确目标核对与显式写入确认流程，见[mqtt.md](mqtt.md)。

测试覆盖标准MessagePack向量、各长度/整数/浮点/扩展类型、重复键/复合键、截断、
尾随、深度/长度恶意输入、模糊测试种子、回环broker真实读取，以及TUI缓存边界、
精度、历史跟随、图表、字段选择、旧缓存删除、主题搜索和关闭/取消边界。
没有连接任何外部broker。
