import 'dart:async';
import 'dart:convert';
import 'dart:math' as math;
import 'package:flutter/material.dart';
import '../../core/json.dart';
import '../../core/session.dart';
import '../../shared/widgets.dart';
import 'messaging_models.dart';
import 'kafka_entities.dart';
import 'retained_preview.dart';

class ResultPage extends StatefulWidget {
  const ResultPage({
    required this.session,
    required this.onPrepare,
    required this.onRerun,
    super.key,
  });
  final AppSession session;
  final ValueChanged<JsonMap> onPrepare;
  final VoidCallback onRerun;
  @override
  State<ResultPage> createState() => _ResultPageState();
}

class _ResultPageState extends State<ResultPage> {
  int visible = 30;
  @override
  Widget build(BuildContext context) {
    final s = widget.session;
    final events = s.events
        .where(
          (e) => ![
            'started',
            'done',
            'connected',
            'subscription.started',
            'history_status',
          ].contains(e['kind']),
        )
        .toList()
        .reversed
        .toList();
    return ListView(
      padding: const EdgeInsets.all(16),
      children: [
        Row(
          children: [
            Expanded(
              child: Text(
                s.status,
                style: Theme.of(context).textTheme.titleLarge,
              ),
            ),
            if (s.running)
              const SizedBox(
                width: 20,
                height: 20,
                child: CircularProgressIndicator(strokeWidth: 2),
              ),
          ],
        ),
        for (final notice in s.events.where((e) => e['kind'] == 'history_status'))
          Padding(
            padding: const EdgeInsets.symmetric(vertical: 8),
            child: Text(
              mapOf(notice['data'])['message']?.toString() ?? '',
              key: const ValueKey('history_recording_notice'),
            ),
          ),
        if (s.dropped > 0 || s.evicted > 0)
          Text(
            '缓冲区淘汰 ${s.dropped} 条事件 · 完整结果缓存淘汰 ${s.evicted} 项',
            style: const TextStyle(color: Colors.amber),
          ),
        ActionWrap(
          children: [
            OutlinedButton.icon(
              onPressed: () => copyText(context, pretty(s.events)),
              icon: const Icon(Icons.copy),
              label: const Text('复制'),
            ),
            OutlinedButton.icon(
              onPressed: () => guarded(
                context,
                () => s.exportText('result.json', exactEncode(s.events)),
              ),
              icon: const Icon(Icons.file_download_outlined),
              label: const Text('导出'),
            ),
            OutlinedButton.icon(
              onPressed: s.running ? null : widget.onRerun,
              icon: const Icon(Icons.replay),
              label: const Text('重新执行'),
            ),
          ],
        ),
        if (events.any((e) => e['kind'] == 'message'))
          FilledButton.icon(
            onPressed: () => Navigator.push(
              context,
              MaterialPageRoute(builder: (_) => MqttBrowser(session: s)),
            ),
            icon: const Icon(Icons.account_tree),
            label: const Text('主题树、消息历史与图表'),
          ),
        if (events.any((e) => e['kind'] == 'record'))
          FilledButton.icon(
            onPressed: () => Navigator.push(
              context,
              MaterialPageRoute(
                builder: (_) =>
                    KafkaBrowser(session: s, onPrepare: widget.onPrepare),
              ),
            ),
            icon: const Icon(Icons.table_rows),
            label: const Text('浏览 Kafka 记录'),
          ),
        if (events.isEmpty)
          const EmptyState('等待执行结果', '请求完成后，正文、响应头与协议结果将显示在这里'),
        ...events
            .take(visible)
            .map(
              (e) =>
                  EventCard(event: e, session: s, onPrepare: widget.onPrepare),
            ),
        if (events.length > visible)
          OutlinedButton(
            onPressed: () => setState(() => visible += 30),
            child: Text('加载更早的结果（剩余 ${events.length - visible}）'),
          ),
        ExpansionTile(
          title: const Text('执行日志'),
          children: s.events
              .where(
                (e) => ['started', 'done', 'connected'].contains(e['kind']),
              )
              .map(
                (e) => ListTile(
                  title: Text('${e['kind']} · ${e['time']}'),
                  subtitle: Text(pretty(e['data'])),
                ),
              )
              .toList(),
        ),
      ],
    );
  }
}

class EventCard extends StatelessWidget {
  const EventCard({
    required this.event,
    required this.session,
    required this.onPrepare,
    super.key,
  });
  final JsonMap event;
  final AppSession session;
  final ValueChanged<JsonMap> onPrepare;
  @override
  Widget build(BuildContext context) {
    final kind = event['kind']?.toString() ?? '',
        data = event['data'],
        d = mapOf(data);
    return Section(
      _eventTitle(kind),
      subtitle: event['time']?.toString(),
      children: [
        if (session.originalResultRequest?['protocol'] == 'kafka' &&
            [
              'topics',
              'brokers',
              'groups',
              'group',
              'lag',
              'offsets',
              'topic-config',
              'response',
            ].contains(kind))
          FilledButton.icon(
            onPressed: () => Navigator.push(
              context,
              MaterialPageRoute(
                builder: (_) => KafkaEntityBrowser(
                  kind: kind,
                  data: data,
                  request: cloneMap(session.originalResultRequest!),
                  onPrepare: onPrepare,
                  exportText: session.exportText,
                ),
              ),
            ),
            icon: const Icon(Icons.account_tree),
            label: const Text('浏览实体 / 分区 / 管理操作'),
          ),
        if (kind == 'response-file') ...[
          Text('已保存 ${d['bytes']} 字节 · SHA256 ${d['sha256']}'),
          OutlinedButton(
            onPressed: () => guarded(context, () async {
              if (session.transfer != null) {
                await session.transfer!('files.export', {
                  'path': d['path'],
                  'name': d['path'].toString().split('/').last,
                });
              } else {
                await session.platform.invoke('files.export', {
                  'path': d['path'],
                });
              }
            }),
            child: const Text('导出下载文件'),
          ),
        ],
        if (d['truncated'] == true) ...[
          Text('结果 ${d['original_bytes']} 字节，仅显示预览'),
          DataView(d['preview']),
          if (d['result_id'] != null)
            OutlinedButton(
              onPressed: () => guarded(context, () async {
                final full = mapOf(
                  await session.command({
                    'op': 'result.get',
                    'result_id': d['result_id'],
                  }),
                );
                if (context.mounted)
                  await memoryDialog(
                    context: context,
                    builder: (c) => AlertDialog(
                      title: const Text('完整结果'),
                      content: SizedBox(
                        width: 760,
                        child: SingleChildScrollView(
                          child: full['kind'] == 'retained-preview'
                              ? RetainedPreview(
                                  snapshot: mapOf(full['data']),
                                  request:
                                      session.originalResultRequest ??
                                      session.draft,
                                  onPrepare: (r) {
                                    onPrepare(r);
                                    Navigator.pop(c);
                                  },
                                )
                              : DataView(full['data']),
                        ),
                      ),
                      actions: [
                        TextButton(
                          onPressed: () => showData(
                            c,
                            '完整原始结果',
                            full['data'],
                            export: session.exportText,
                          ),
                          child: const Text('复制 / 导出'),
                        ),
                        TextButton(
                          onPressed: () => Navigator.pop(c),
                          child: const Text('关闭'),
                        ),
                      ],
                    ),
                  );
              }),
              child: const Text('查看完整结果 / 导出'),
            ),
        ] else if (kind == 'response' || kind == 'transformed')
          HttpResponseView(data: d)
        else if (kind == 'message') ...[
          Text(
            d['topic']?.toString() ?? '',
            style: Theme.of(context).textTheme.titleSmall,
          ),
          Text('QoS ${d['qos']} · ${d['retained'] == true ? '保留消息' : '实时消息'}'),
          DataView(
            d['payload_json'] ?? d['payload_messagepack'] ?? d['payload'] ?? d,
          ),
        ] else if (kind == 'record') ...[
          Text('${d['topic']} / 分区 ${d['partition']} / 偏移 ${d['offset']}'),
          DataView(d['value']),
        ] else if (kind == 'retained-preview')
          RetainedPreview(
            snapshot: d,
            request: session.originalResultRequest ?? session.draft,
            onPrepare: onPrepare,
          )
        else if ([
          'topic',
          'topics',
          'group',
          'groups',
          'lag',
          'brokers',
          'reference',
          'endpoint',
          'schema',
          'schemas',
          'connectors',
        ].contains(kind))
          _drilldown(context, kind, data)
        else
          DataView(data),
        TextButton(
          onPressed: () =>
              showData(context, '原始 $kind', data, export: session.exportText),
          child: const Text('原始数据 / 复制 / 导出'),
        ),
      ],
    );
  }

  Widget _drilldown(BuildContext context, String kind, Object? data) {
    final values = data is List
        ? data
        : mapOf(data)['topics'] is List
        ? mapOf(data)['topics'] as List
        : mapOf(data)['groups'] is List
        ? mapOf(data)['groups'] as List
        : [data];
    return Column(
      children: values.take(80).map((v) {
        final d = mapOf(v);
        final name =
            d['name'] ??
            d['topic'] ??
            d['group'] ??
            d['display_name'] ??
            d['node_id'] ??
            d['endpoint'] ??
            v;
        return ListTile(
          title: Text(name.toString()),
          subtitle: d.isEmpty
              ? null
              : Text(pretty(d), maxLines: 4, overflow: TextOverflow.ellipsis),
          trailing: const Icon(Icons.chevron_right),
          onTap: () {
            final r = cloneMap(session.originalResultRequest ?? session.draft),
                p = mapOf(r['params']);
            if (r['protocol'] == 'opcua') {
              r['action'] =
                  d['node_class'].toString().toLowerCase().contains('method')
                  ? 'method-arguments'
                  : 'browse';
              p['node_id'] = d['node_id'] ?? name;
            } else if (kind.contains('group') || kind == 'lag') {
              r['action'] = 'lag';
              p['group'] = d['group'] ?? name;
            } else if (kind.contains('schema')) {
              r['action'] = 'schema-versions';
              p['subject'] = name;
            } else if (kind.contains('connector')) {
              r['action'] = 'connector';
              p['connector'] = name;
            } else {
              r['action'] = 'consume';
              p['topic'] = d['topic'] ?? name;
              p['limit'] = 100;
            }
            r['params'] = p;
            onPrepare(r);
          },
        );
      }).toList(),
    );
  }
}

String _eventTitle(String kind) =>
    const {
      'response': 'HTTP 响应',
      'transformed': '转换后响应',
      'message': 'MQTT 消息',
      'record': 'Kafka 记录',
      'registers': '寄存器',
      'bits': '线圈 / 离散输入',
      'query': '查询结果',
      'error': '错误',
      'retained-preview': '保留消息清理预览',
      'subscribed': '订阅已建立',
      'reconnecting': '正在重连',
      'reconnected': '已重新连接',
      'published': '发布结果',
    }[kind] ??
    kind;

class HttpResponseView extends StatefulWidget {
  const HttpResponseView({required this.data, super.key});
  final JsonMap data;
  @override
  State<HttpResponseView> createState() => _HttpResponseViewState();
}

class _HttpResponseViewState extends State<HttpResponseView> {
  int tab = 0;
  @override
  Widget build(BuildContext context) => Column(
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      Text(
        '状态 ${widget.data['status'] ?? ''} ${widget.data['duration_ms'] != null ? '· ${widget.data['duration_ms']} ms' : ''}',
        style: Theme.of(context).textTheme.titleMedium,
      ),
      const SizedBox(height: 12),
      SegmentedButton<int>(
        segments: const [
          ButtonSegment(value: 0, label: Text('正文')),
          ButtonSegment(value: 1, label: Text('响应头')),
          ButtonSegment(value: 2, label: Text('原始')),
        ],
        selected: {tab},
        onSelectionChanged: (v) => setState(() => tab = v.first),
      ),
      const SizedBox(height: 12),
      DataView(
        tab == 0
            ? widget.data['body']
            : tab == 1
            ? widget.data['headers']
            : widget.data,
      ),
    ],
  );
}

class MessagingTools extends StatelessWidget {
  const MessagingTools({
    required this.session,
    required this.onPrepare,
    super.key,
  });
  final AppSession session;
  final ValueChanged<JsonMap> onPrepare;
  @override
  Widget build(BuildContext context) => Section(
    session.draft['protocol'] == 'mqtt' ? 'MQTT 主题工作区' : 'Kafka 数据工作区',
    children: [
      OutlinedButton(
        onPressed: () => Navigator.push(
          context,
          MaterialPageRoute(
            builder: (_) => session.draft['protocol'] == 'mqtt'
                ? MqttBrowser(session: session)
                : KafkaBrowser(session: session, onPrepare: onPrepare),
          ),
        ),
        child: Text(
          session.draft['protocol'] == 'mqtt'
              ? '主题树 / 消息 / 数值图表'
              : '记录筛选 / 分页 / 偏移草稿',
        ),
      ),
      const Text('这里浏览已接收的数据；切换、搜索、复制和清空缓存不会扩大订阅范围或发送请求。'),
    ],
  );
}

class MqttBrowser extends StatefulWidget {
  const MqttBrowser({required this.session, super.key});
  final AppSession session;
  @override
  State<MqttBrowser> createState() => _MqttBrowserState();
}

class _MqttBrowserState extends State<MqttBrowser> {
  final cache = TopicCache();
  late StreamSubscription<JsonMap> sub;
  late String scope;
  String search = '', selected = '', selector = 'payload';
  bool all = true, follow = true;
  final Set<String> expanded = {};
  List<JsonMap>? frozen;
  Timer? redraw;
  @override
  void initState() {
    super.initState();
    scope = '${widget.session.resultRunId}';
    for (final e in widget.session.events) {
      accept(e);
    }
    sub = widget.session.eventStream.listen(accept);
    redraw = Timer.periodic(const Duration(seconds: 1), (_) {
      if (mounted && follow) setState(() {});
    });
  }

  void accept(JsonMap e) {
    if (e['kind'] == 'message' && e['run_id'] == scope) cache.add(e);
  }

  @override
  void dispose() {
    sub.cancel();
    redraw?.cancel();
    super.dispose();
  }

  List<JsonMap> get current =>
      (frozen ??
            cache.topics.entries
                .where((e) => all || e.key == selected)
                .expand((e) => e.value)
                .toList())
        ..sort((a, b) => a['time'].toString().compareTo(b['time'].toString()));
  @override
  Widget build(BuildContext context) {
    final topics = cache.topics.keys.where((t) => t.contains(search)).toList()
      ..sort();
    final nodes = <String>{};
    for (final t in topics) {
      nodes.addAll(topicPrefixes(t));
    }
    final messages = current;
    final selectors = <String>{'payload', 'bytes', 'rate', 'byte[0]'};
    for (final e in messages) {
      selectors.addAll(
        numericFields(
          _payload(mapOf(e['data'])),
          messagepack: mapOf(e['data'])['payload_format'] == 'messagepack',
        ).keys,
      );
    }
    selectors.add(selector);
    final points = List<double>.generate(
      messages.length,
      (i) => point(messages, i, selector),
    ).where((x) => x.isFinite).toList();
    return Scaffold(
      appBar: AppBar(
        title: const Text('MQTT 主题与历史'),
        actions: [
          IconButton(
            tooltip: '清空缓存',
            onPressed: () async {
              if (await confirm(
                    context,
                    '清空本地消息缓存？',
                    '不会删除 broker 上的消息或改变订阅。',
                  ) &&
                  mounted)
                setState(() {
                  cache.clear();
                  frozen?.clear();
                });
            },
            icon: const Icon(Icons.delete_sweep_outlined),
          ),
        ],
      ),
      body: LayoutBuilder(
        builder: (c, box) {
          final tree = Column(
            children: [
              TextField(
                decoration: const InputDecoration(labelText: '搜索精确主题'),
                onChanged: (v) => setState(() => search = v),
              ),
              ListTile(
                title: Text('全部主题 · ${cache.count} 条'),
                selected: all,
                onTap: () => setState(() {
                  all = true;
                  frozen = null;
                }),
              ),
              Expanded(
                child: ListView(
                  children: nodes
                      .where((n) {
                        final ps = topicPrefixes(n);
                        return search.isNotEmpty ||
                            ps.take(ps.length - 1).every(expanded.contains);
                      })
                      .map((n) {
                        final count = cache.topics[n]?.length ?? 0;
                        final children = nodes.any(
                          (x) => x != n && topicUnder(x, n),
                        );
                        return ListTile(
                          contentPadding: EdgeInsets.only(
                            left: 8 + topicPrefixes(n).length * 12.0,
                            right: 8,
                          ),
                          leading: children
                              ? IconButton(
                                  tooltip: '展开 / 折叠',
                                  onPressed: () => setState(
                                    () => expanded.contains(n)
                                        ? expanded.remove(n)
                                        : expanded.add(n),
                                  ),
                                  icon: Icon(
                                    expanded.contains(n)
                                        ? Icons.expand_more
                                        : Icons.chevron_right,
                                  ),
                                )
                              : const Icon(Icons.tag, size: 20),
                          title: Text(n.isEmpty ? '（空层级 /）' : n),
                          subtitle: count == 0 ? null : Text('$count 条'),
                          selected: !all && selected == n,
                          onTap: () => setState(() {
                            selected = n;
                            all = false;
                            frozen = null;
                          }),
                        );
                      })
                      .toList(),
                ),
              ),
            ],
          );
          final history = Column(
            children: [
              Row(
                children: [
                  Expanded(child: Text(all ? '全部消息' : selected)),
                  Switch(
                    value: follow,
                    onChanged: (v) => setState(() {
                      follow = v;
                      frozen = v ? null : List.of(current);
                    }),
                  ),
                  Text(follow ? '跟随' : '冻结'),
                ],
              ),
              Text(
                '丢弃 ${cache.dropped} 条 · ${(cache.bytes / 1024).toStringAsFixed(0)} KiB · 只保留有界缓存',
              ),
              ChoiceField(
                key: ValueKey(selector),
                label: '图表指标',
                value: selector,
                options: selectors.toList(),
                onChanged: (v) => setState(() => selector = v),
              ),
              if (selector.startsWith('byte'))
                TextButton(
                  onPressed: () async {
                    final index = await inputDialog(
                      context,
                      '字节索引',
                      initial: '0',
                    );
                    final n = int.tryParse(index ?? '');
                    if (n != null && n >= 0 && n < 1048576)
                      setState(() => selector = 'byte[$n]');
                  },
                  child: const Text('选择字节索引'),
                ),
              SizedBox(
                height: 150,
                child: CustomPaint(
                  painter: SeriesPainter(points),
                  child: const SizedBox.expand(),
                ),
              ),
              if (points.isNotEmpty)
                Text(
                  '最小 ${points.reduce(math.min)} · 最大 ${points.reduce(math.max)} · ${points.length} 个点',
                ),
              ActionWrap(
                children: [
                  TextButton(
                    onPressed: () => guarded(
                      context,
                      () => widget.session.exportText(
                        'mqtt-history.json',
                        exactEncode(messages),
                      ),
                    ),
                    child: const Text('导出当前历史'),
                  ),
                  TextButton(
                    onPressed: all
                        ? null
                        : () => setState(() {
                            cache.clearTopic(selected);
                            frozen?.removeWhere(
                              (e) => mapOf(e['data'])['topic'] == selected,
                            );
                          }),
                    child: const Text('清空当前精确主题'),
                  ),
                ],
              ),
              Expanded(
                child: ListView.builder(
                  itemCount: messages.length,
                  itemBuilder: (c, i) {
                    final e = messages[messages.length - 1 - i],
                        d = mapOf(e['data']);
                    return Card(
                      child: ListTile(
                        title: Text(d['topic'].toString()),
                        subtitle: Text(
                          '${e['time']}\n${d['payload'] ?? d['payload_json'] ?? d}',
                          maxLines: 5,
                          overflow: TextOverflow.ellipsis,
                        ),
                        onTap: () => showData(
                          c,
                          '消息详情',
                          e,
                          export: widget.session.exportText,
                        ),
                        trailing: IconButton(
                          tooltip: '删除此条缓存',
                          onPressed: () => setState(() {
                            cache.deleteMessage(
                              d['topic'].toString(),
                              e['seq'],
                            );
                            frozen?.removeWhere(
                              (x) =>
                                  x['seq'] == e['seq'] &&
                                  x['run_id'] == e['run_id'],
                            );
                          }),
                          icon: const Icon(Icons.close),
                        ),
                      ),
                    );
                  },
                ),
              ),
            ],
          );
          return Padding(
            padding: const EdgeInsets.all(12),
            child: box.maxWidth > 700
                ? Row(
                    children: [
                      SizedBox(width: 280, child: tree),
                      const VerticalDivider(),
                      Expanded(child: history),
                    ],
                  )
                : Column(
                    children: [
                      SizedBox(height: 220, child: tree),
                      const Divider(),
                      Expanded(child: history),
                    ],
                  ),
          );
        },
      ),
    );
  }
}

Object? _payload(JsonMap d) {
  if (d['payload_json'] != null) return d['payload_json'];
  if (d['payload_messagepack'] != null) return d['payload_messagepack'];
  try {
    return exactDecode(d['payload']?.toString() ?? '');
  } catch (_) {
    return d['payload'];
  }
}

double point(List<JsonMap> events, int i, String key) {
  final d = mapOf(events[i]['data']);
  if (d['retained'] == true) return double.nan;
  List<int> bytes;
  try {
    bytes = base64Decode(
      d['payload_base64']?.toString() ??
          d['raw_payload_base64']?.toString() ??
          '',
    );
    if (bytes.isEmpty) bytes = utf8.encode(d['payload']?.toString() ?? '');
  } catch (_) {
    bytes = utf8.encode(d['payload']?.toString() ?? '');
  }
  if (key == 'bytes') return bytes.length.toDouble();
  if (key == 'rate') {
    if (i == 0) return double.nan;
    var previous = i - 1;
    while (previous >= 0 &&
        mapOf(events[previous]['data'])['retained'] == true) {
      previous--;
    }
    if (previous < 0) return double.nan;
    final a = DateTime.tryParse(events[i]['time'].toString()),
        b = DateTime.tryParse(events[previous]['time'].toString());
    if (a == null || b == null) return double.nan;
    final delta = a.difference(b).inMicroseconds;
    return delta > 0 ? 1000000 / delta : double.nan;
  }
  if (key.startsWith('byte[')) {
    final index = int.tryParse(key.substring(5, key.length - 1)) ?? 0;
    return index < bytes.length ? bytes[index].toDouble() : double.nan;
  }
  final fields = numericFields(
    _payload(d),
    messagepack: d['payload_format'] == 'messagepack',
  );
  return (fields[key == 'payload' ? r'$' : key] ?? double.nan).toDouble();
}

class SeriesPainter extends CustomPainter {
  SeriesPainter(this.points);
  final List<double> points;
  @override
  void paint(Canvas canvas, Size size) {
    final paint = Paint()
      ..color = cyan
      ..strokeWidth = 2
      ..style = PaintingStyle.stroke;
    if (points.length < 2) return;
    final min = points.reduce(math.min), max = points.reduce(math.max);
    final scale = math.max(min.abs(), max.abs());
    final low = scale == 0 ? 0 : min / scale,
        high = scale == 0 ? 0 : max / scale;
    final range = max == min ? 1 : high - low;
    final path = Path();
    for (var i = 0; i < points.length; i++) {
      final x = i * size.width / (points.length - 1),
          y =
              size.height -
              12 -
              ((scale == 0 ? 0 : points[i] / scale) - low) /
                  range *
                  (size.height - 24);
      if (i == 0)
        path.moveTo(x, y);
      else
        path.lineTo(x, y);
    }
    canvas.drawPath(path, paint);
  }

  @override
  bool shouldRepaint(covariant SeriesPainter old) => old.points != points;
}

class KafkaBrowser extends StatefulWidget {
  const KafkaBrowser({
    required this.session,
    required this.onPrepare,
    super.key,
  });
  final AppSession session;
  final ValueChanged<JsonMap> onPrepare;
  @override
  State<KafkaBrowser> createState() => _KafkaBrowserState();
}

class _KafkaBrowserState extends State<KafkaBrowser> {
  String query = '', sort = 'offset';
  int page = 0;
  bool ascending = true;
  @override
  Widget build(BuildContext context) {
    final records =
        widget.session.events
            .where(
              (e) =>
                  e['kind'] == 'record' &&
                  pretty(e['data']).toLowerCase().contains(query.toLowerCase()),
            )
            .toList()
          ..sort(
            (a, b) =>
                (ascending ? 1 : -1) *
                compareExact(kafkaOrderKey(a, sort), kafkaOrderKey(b, sort)),
          );
    return Scaffold(
      appBar: AppBar(title: const Text('Kafka 记录浏览器')),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              children: [
                TextField(
                  decoration: const InputDecoration(labelText: '本地搜索键、值、主题或头'),
                  onChanged: (v) => setState(() {
                    query = v;
                    page = 0;
                  }),
                ),
                const SizedBox(height: 8),
                Row(
                  children: [
                    Expanded(
                      child: ChoiceField(
                        label: '排序',
                        value: sort,
                        options: const [
                          'offset',
                          'timestamp',
                          'partition',
                          'topic',
                          'key',
                        ],
                        onChanged: (v) => setState(() => sort = v),
                      ),
                    ),
                    IconButton(
                      tooltip: '切换排序方向',
                      onPressed: () => setState(() => ascending = !ascending),
                      icon: Icon(
                        ascending ? Icons.arrow_upward : Icons.arrow_downward,
                      ),
                    ),
                  ],
                ),
                Text('${records.length} 条有界缓存记录 · 不发起额外消费'),
              ],
            ),
          ),
          Expanded(
            child: ListView(
              children: records.skip(page * 30).take(30).map((e) {
                final d = mapOf(e['data']);
                return Card(
                  child: ListTile(
                    title: Text(
                      '${d['topic']} / ${d['partition']} / ${d['offset']}',
                    ),
                    subtitle: Text(
                      '${d['timestamp']}\n${d['key']} → ${d['value']}',
                      maxLines: 4,
                      overflow: TextOverflow.ellipsis,
                    ),
                    onTap: () => detail(records, records.indexOf(e)),
                  ),
                );
              }).toList(),
            ),
          ),
          Row(
            mainAxisAlignment: MainAxisAlignment.center,
            children: [
              IconButton(
                onPressed: page > 0 ? () => setState(() => page--) : null,
                icon: const Icon(Icons.chevron_left),
              ),
              Text('第 ${page + 1} 页'),
              IconButton(
                onPressed: (page + 1) * 30 < records.length
                    ? () => setState(() => page++)
                    : null,
                icon: const Icon(Icons.chevron_right),
              ),
            ],
          ),
        ],
      ),
    );
  }

  Future<void> detail(List<JsonMap> snapshot, int selected) async {
    var index = selected;
    final prepared = await memoryDialog<JsonMap>(
      context: context,
      builder: (c) => StatefulBuilder(
        builder: (c, set) {
          final data = mapOf(snapshot[index]['data']);
          return AlertDialog(
            title: Text('记录 ${index + 1} / ${snapshot.length}'),
            content: SizedBox(
              width: 700,
              child: SingleChildScrollView(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    Text(
                      '${data['topic']} / 分区 ${data['partition']} / 偏移 ${data['offset']}',
                    ),
                    Text('时间 ${data['timestamp']}'),
                    if (data['key_is_null'] == true) const Text('Key = null'),
                    if (data['value_is_null'] == true)
                      const Text('Tombstone：Value = null'),
                    const SizedBox(height: 12),
                    const Text('解码键 / 值'),
                    DataView({'key': data['key'], 'value': data['value']}),
                    ExpansionTile(
                      title: const Text('消息头'),
                      children: [DataView(data['headers'])],
                    ),
                    ExpansionTile(
                      title: const Text('原始二进制 Base64'),
                      initiallyExpanded: true,
                      children: [
                        DataView({
                          'raw_key_base64': data['raw_key_base64'],
                          'raw_value_base64': data['raw_value_base64'],
                        }),
                      ],
                    ),
                  ],
                ),
              ),
            ),
            actions: [
              IconButton(
                tooltip: '上一条缓存记录',
                onPressed: index > 0 ? () => set(() => index--) : null,
                icon: const Icon(Icons.chevron_left),
              ),
              IconButton(
                tooltip: '下一条缓存记录',
                onPressed: index + 1 < snapshot.length
                    ? () => set(() => index++)
                    : null,
                icon: const Icon(Icons.chevron_right),
              ),
              TextButton(
                onPressed: () => showData(
                  c,
                  '完整记录',
                  data,
                  export: widget.session.exportText,
                ),
                child: const Text('复制 / 导出'),
              ),
              OutlinedButton(
                onPressed: () {
                  final r = cloneMap(
                    widget.session.originalResultRequest ??
                        widget.session.draft,
                  );
                  r['action'] = 'consume';
                  r['params'] = {
                    ...mapOf(r['params']),
                    'topic': data['topic'],
                    'partition': data['partition'],
                    'offset': data['offset'],
                    'limit': 100,
                  };
                  Navigator.pop(c, r);
                },
                child: const Text('从此偏移准备消费'),
              ),
              TextButton(
                onPressed: () => Navigator.pop(c),
                child: const Text('关闭'),
              ),
            ],
          );
        },
      ),
    );
    if (prepared != null && mounted) {
      widget.onPrepare(prepared);
      Navigator.pop(context);
    }
  }
}
