# MQTT：保留主题预览、确认清理与订阅恢复

本模块使用纯 Go 的 Eclipse Paho 客户端，直接通过 MQTT 3.1.1 连接 broker，
无需外部 mqttui 或 mosquitto 命令。参考 [mqttui 的公开工作流](https://github.com/EdJoPaTo/mqttui#clean-retained-topics)
独立实现，未复制其 GPL 应用源码。

## 基础操作

- `publish`：一个不含通配符的精确主题；支持 `qos: 0|1|2`、`retain`，以及
  `payload_encoding: text|hex|base64`。发布和清理均需要明确的写入确认
- `subscribe`：`topic` 或 `topics` 列表；支持 `+`、`#`，默认收取 100 条
- `read-one`：得到第一条符合条件的消息后退出
- `ignore_retained: true`：订阅/单条读取时忽略 broker 回放的保留消息
- 每条消息同时保留文本、HEX、Base64、QoS、保留标记、字节数。合法 JSON
  还提供保留大整数精度的解析结果；非 UTF-8 载荷不会被当作有效文本

端点支持 mqtt://、mqtts://、tcp://、ssl://、ws://、wss://。用户名/密码放在
参数中，可引用环境变量；不要嵌入 URL。TLS/mTLS 继续执行证书校验。
每次操作默认使用随机 client ID、clean session；手动指定相同 client ID
可能使 broker 断开另一个正在运行的客户端。

## 递归清理必须先预览

MQTT 没有“删除一个通配主题”的报文。清理是向每个精确主题发布空载荷且
设置 retain。为了防止过滤器意外扩大范围，通配或多主题清理必须经过以下步骤。
单个精确主题仍可直接使用 `clean-retained`，但也可选择相同的预览保护流程。

### 1. 运行只读预览

```yaml
version: 1
requests:
  - id: preview-retained
    protocol: mqtt
    action: preview-retained
    endpoint: mqtt://127.0.0.1:1883
    timeout: 10s
    params:
      topic: factory/test/#
      qos: 1
      scan_duration_ms: 1000
      max_topics: 1000
```

```sh
iotools --file mqtt.yaml --run preview-retained
```

此操作只连接/订阅，不发布消息。它先输出已观察到的保留消息，最后输出
`retained-preview` 事件，其中包括：

- `topics`：排序且去重后的精确主题列表
- `count`：主题数
- `filters`：原来的过滤器列表
- `confirm_token`：绑定端点、过滤器、精确主题、载荷摘要及接收 QoS 的确认值
- `scan_duration_ms`：本次 SUBACK 后的观察窗口
- `bounded_snapshot: true`：明确表示这是有界观察，而非完整、原子的数据库快照

重叠过滤器不会重复计数；普通非保留实时消息不会进入清理列表。默认 `#`
不包含以 `$` 开头的系统主题，需要按 broker 规则显式指定系统主题过滤器。

在 TUI 中，扫描成功结束后会打开可滚动的精确主题列表。使用方向键或翻页键
检查全部主题，Tab/Shift-Tab 在列表及按钮之间切换，Esc 或“取消”放弃。
只读模式只提供查看/关闭；不会显示清理按钮。扫描尚在运行、失败或被取消时，
不会弹出可执行清理的预览。主题中的颜色标记按原文显示，不能隐藏清理范围。

### 2. 核对主题并确认写入

复制本次预览中的精确列表和确认值，保持端点、过滤器及 QoS 不变。例如：

```yaml
  - id: cleanup-retained
    protocol: mqtt
    action: clean-retained
    endpoint: mqtt://127.0.0.1:1883
    timeout: 10s
    params:
      topic: factory/test/#
      qos: 1
      scan_duration_ms: 1000
      max_topics: 1000
      confirm_topics:
        - factory/test/a
        - factory/test/area/b
      confirm_token: 替换为刚才预览得到的64位十六进制值
```

```sh
iotools --file mqtt.yaml --run cleanup-retained --allow-writes
```

`confirm_token` 不是密码，也不能替代 `--allow-writes` 或 TUI 写入确认。
它防止不小心使用已过时或来自其他端点/过滤器的预览。不要照抄示例主题或确认值。

### 3. 重新检查，再逐项清理

执行清理时会重新连接、重新订阅，并使用相同观察窗口再次建立清单。如果精确
主题列表、任何载荷摘要或接收 QoS 与确认不一致，整个批次在首次写入前失败。
失败事件会显示新的预览，必须重新核对后确认，不能自动接受扩大后的列表。

只有核对一致才依次发送空保留载荷，清理 QoS 至少为 1，以等待 broker 确认。
每个收到确认的删除输出 `retained-cleaned`；全部完成输出
`retained-cleanup-complete`。断线、取消或超时会停止后续发布，不会自动重放
删除批次。错误会报告已确认完成数量；中断时正在发送的那个主题结果可能未知，
需要重新预览。空列表可确认，结果是安全地完成 0 个主题。

### MQTT 协议限制与安全边界

MQTT 3.1.1 没有保留消息结束标记，也没有“载荷仍等于 X 才删除”的原子操作。
慢速 broker、网络延迟或并发发布都可能使一次有界扫描遗漏主题。第二次核对
缩小了风险，但不能消除最后核对与发布空载荷之间的竞争。清理前请暂停相关
保留消息发布者，并在需要时增加观察窗口；遗漏主题不会被当作通配目标删除。
不要将此流程当作备份或具有事务回滚保证的清理工具。

- `scan_duration_ms`：100..30000 毫秒，默认 500
- `max_topics`：1..10000，默认 1000；超限直接失败，绝不静默截断后删除
- 预览主题名与载荷累计最多 8 MiB，单条最多 4 MiB
- `timeout` 必须覆盖连接、订阅、整个扫描和后续发布时间
- 预览发生断线、取消、超时或容量超限时，不输出可用于清理的完整确认事件
- `confirm_topics` 必须是无重复、无通配符的精确主题；允许空列表

## 自动重连与重订阅

`subscribe` 和 `read-one` 默认 `auto_reconnect: true`。在至少一次成功订阅后，
连接意外中断会等待 `reconnect_interval_ms`（默认 500，允许 100..30000），
创建新客户端、重新连接，并重新订阅原来的所有过滤器和 QoS。每次恢复都仍
保留原来的 `ignore_retained` 设置、累计消息上限及原请求总超时。

```yaml
params:
  topics: [devices/+/value, alarms/#]
  qos: 1
  limit: 500
  ignore_retained: true
  auto_reconnect: true
  reconnect_interval_ms: 1000
```

首次连接/订阅失败会立即返回错误；恢复期间的连接失败会继续有界重试直到
总超时或取消。`auto_reconnect: false` 可恢复断线即退出行为。`reconnecting`
事件显示尝试次数及等待时间；`reconnected` 后再次输出 `subscribed`。
停止/取消会中断重试等待；任务完成后没有后台重连。发布、保留预览与保留清理
不会自动重试，从而避免隐式重复写入或把断线两侧的观察合并为一份删除清单。

这是诊断客户端的 clean-session 恢复，不是持久会话：断线期间的实时消息可能
丢失，重订阅时保留消息可能再次回放并计入 `limit`。需要只看实时消息时使用
`ignore_retained`；业务上需要可靠离线投递时，应使用独立的持久业务消费者。

## 验证范围

自动测试只启动回环地址、随机端口、无持久化的临时 MQTT broker，覆盖：

- 原有真实发布/读取/精确清理与二进制无损表示
- 通配及重叠过滤器预览、超过 32 个保留主题、实时消息排除
- 缺少确认/无写权限拒绝、确认后清理、范围外主题保持
- 新增/删除主题、载荷变化、端点/过滤器/确认列表变化时整批拒绝
- 容量边界、空快照、预览断线/取消/超时
- 多次断线/临时 broker 不可用后的全部过滤器恢复、累计 limit、原请求超时、
  read-one、关闭重连及取消等待
- TUI 扫描完成后才展示、只读/取消/再次确认、长列表滚动、缩放、主题标记转义

这些是原生客户端与临时 broker 的协议测试，不代表已经连接真实生产 broker
或验证其 ACL、持久存储和部署特有行为。

## 主题历史、图表与MessagePack

现已提供主题树 `h` 历史、`g` 图表、`/` 主题搜索、`o`/`O` 展开收起；历史面板中
`s` 选择字段或二进制字节，Delete仅移除较早本机缓存。非UTF-8载荷可自动识别
有界MessagePack，并保留原始HEX/Base64。详见[历史与图表操作、边界和源码对照](mqtt-history.md)。
