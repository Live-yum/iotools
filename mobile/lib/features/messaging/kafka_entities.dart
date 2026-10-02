import 'package:flutter/material.dart';
import '../../core/json.dart';
import '../../shared/widgets.dart';

Object? kafkaValue(JsonMap map, String key) => map.entries
    .where((e) => e.key.toLowerCase() == key.toLowerCase())
    .firstOrNull
    ?.value;
List<JsonMap> kafkaEntities(String kind, Object? value, JsonMap request) {
  final action = request['action']?.toString() ?? kind;
  Object? data = kind == 'response' ? mapOf(value)['body'] : value;
  if (data is String) {
    try {
      data = exactDecode(data);
    } catch (_) {
      return [];
    }
  }
  if (action == 'schema-versions' && data is List)
    return data
        .map(
          (v) => <String, dynamic>{
            'name': '版本 $v',
            'version': v,
            'subject': mapOf(request['params'])['subject'],
          },
        )
        .toList();
  if (data is List)
    return data.map((v) {
      if (v is Map) {
        final m = mapOf(v);
        return <String, dynamic>{
          ...m,
          'name':
              kafkaValue(m, 'name') ??
              kafkaValue(m, 'topic') ??
              kafkaValue(m, 'group') ??
              kafkaValue(m, 'nodeid') ??
              v.toString(),
        };
      }
      return <String, dynamic>{'name': v.toString()};
    }).toList();
  final m = mapOf(data);
  if (m.isEmpty) return [];
  if ([
        'topics',
        'groups',
        'group',
        'lag',
        'connectors',
        'offsets',
      ].contains(action) ||
      ['topics', 'groups', 'group', 'lag', 'offsets'].contains(kind)) {
    return m.entries
        .map((e) => <String, dynamic>{...mapOf(e.value), 'name': e.key})
        .toList();
  }
  return [
    {
      ...m,
      'name':
          m['subject'] ??
          m['name'] ??
          mapOf(request['params'])['subject'] ??
          mapOf(request['params'])['connector'] ??
          action,
    },
  ];
}

JsonMap kafkaDraft(JsonMap original, String action, JsonMap row) {
  final request = cloneMap(original), p = mapOf(request['params']);
  for (final k in [
    'json',
    'body',
    'configs',
    'key',
    'value',
    'partition',
    'consume_partitions',
    'offset',
    'start_time',
    'confirm_subject',
  ]) {
    p.remove(k);
  }
  final name = row['name']?.toString() ?? '';
  request['action'] = action;
  if (['group', 'lag', 'delete-group'].contains(action)) {
    p['group'] = name;
    p['groups'] = [name];
  } else if ([
    'schema',
    'schema-versions',
    'register-schema',
    'delete-schema',
    'delete-subject',
    'purge-subject',
  ].contains(action)) {
    p['subject'] = row['subject'] ?? name;
    if (row['version'] != null)
      p['version'] = row['version'];
    else if (action == 'schema')
      p['version'] = 'latest';
  } else if (action.contains('connector')) {
    p['connector'] = name;
    if (action == 'update-connector')
      p['json'] =
          mapOf(row['info'])['config'] ?? row['config'] ?? <String, dynamic>{};
  } else if (action != 'brokers') {
    p['topic'] = name;
    if (action == 'consume') {
      p['offset'] = 'earliest';
      p['limit'] = 100;
    }
    if (action == 'expand-partitions') p['partitions'] = 1;
  }
  request['params'] = p;
  return request;
}

class KafkaEntityBrowser extends StatefulWidget {
  const KafkaEntityBrowser({
    required this.kind,
    required this.data,
    required this.request,
    required this.onPrepare,
    required this.exportText,
    super.key,
  });
  final String kind;
  final Object? data;
  final JsonMap request;
  final ValueChanged<JsonMap> onPrepare;
  final Future<void> Function(String, String) exportText;
  @override
  State<KafkaEntityBrowser> createState() => _KafkaEntityBrowserState();
}

class _KafkaEntityBrowserState extends State<KafkaEntityBrowser> {
  String search = '';
  int page = 0;
  @override
  Widget build(BuildContext context) {
    final rows =
        kafkaEntities(widget.kind, widget.data, widget.request)
            .where(
              (r) => pretty(r).toLowerCase().contains(search.toLowerCase()),
            )
            .toList()
          ..sort(
            (a, b) => a['name'].toString().compareTo(b['name'].toString()),
          );
    return Scaffold(
      appBar: AppBar(title: Text('Kafka · ${widget.request['action']}')),
      body: Column(
        children: [
          Padding(
            padding: const EdgeInsets.all(16),
            child: TextField(
              decoration: const InputDecoration(labelText: '筛选已读取的实体'),
              onChanged: (v) => setState(() {
                search = v;
                page = 0;
              }),
            ),
          ),
          Text('${rows.length} 项 · 选择只打开本地详情'),
          Expanded(
            child: ListView(
              children: rows
                  .skip(page * 50)
                  .take(50)
                  .map(
                    (r) => Card(
                      child: ListTile(
                        title: Text(r['name'].toString()),
                        subtitle: Text(
                          _description(r),
                          maxLines: 3,
                          overflow: TextOverflow.ellipsis,
                        ),
                        trailing: const Icon(Icons.chevron_right),
                        onTap: () => detail(r),
                      ),
                    ),
                  )
                  .toList(),
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
                onPressed: (page + 1) * 50 < rows.length
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

  String _description(JsonMap row) {
    final parts = kafkaValue(row, 'partitions');
    if (parts is Map) return '${parts.length} 个分区';
    final status = mapOf(row['status']);
    if (status.isNotEmpty)
      return mapOf(status['connector'])['state']?.toString() ?? pretty(status);
    return pretty({...row}..remove('name'));
  }

  Future<void> detail(JsonMap row) async {
    final a = widget.request['action'].toString();
    final options = a.contains('schema') || a == 'schemas'
        ? <String, String>{
            'schema-versions': '版本列表',
            'schema': '读取 Schema',
            'register-schema': '准备注册 Schema',
            'delete-schema': '准备删除版本',
            'delete-subject': '准备软删除 Subject',
            'purge-subject': '准备永久清除 Subject',
          }
        : a.contains('connector')
        ? {
            'connector': '读取状态与任务',
            'update-connector': '准备更新配置',
            'pause-connector': '准备暂停',
            'resume-connector': '准备恢复',
            'delete-connector': '准备删除 Connector',
          }
        : a.contains('group') || a == 'lag'
        ? {'group': '读取成员详情', 'lag': '读取该组 Lag', 'delete-group': '准备删除消费组'}
        : a == 'brokers'
        ? <String, String>{}
        : {
            'consume': '准备消费记录',
            'offsets': '读取分区偏移',
            'topic-config': '读取 Topic 配置',
            'alter-topic': '准备更改配置',
            'expand-partitions': '准备扩充分区',
            'delete-topic': '准备删除 Topic',
          };
    final prepared = await memoryDialog<JsonMap>(
      context: context,
      builder: (c) => AlertDialog(
        title: Text(row['name'].toString()),
        content: SizedBox(
          width: 720,
          child: SingleChildScrollView(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                ...entityDetails(c, row),
                ExpansionTile(
                  title: const Text('完整原始实体'),
                  children: [DataView(row)],
                ),
                const SizedBox(height: 12),
                for (final e in options.entries)
                  OutlinedButton(
                    onPressed: () async {
                      final next = kafkaDraft(widget.request, e.key, row);
                      if (e.key == 'purge-subject') {
                        final typed = await inputDialog(
                          c,
                          '输入完整 Subject 名称以准备永久清除',
                        );
                        if (typed != row['name']) {
                          if (c.mounted) reportError(c, '名称不匹配，未准备永久删除');
                          return;
                        }
                        mapOf(next['params'])['confirm_subject'] = typed;
                        next['params'] = {
                          ...mapOf(next['params']),
                          'confirm_subject': typed,
                        };
                      }
                      if (c.mounted) {
                        Navigator.pop(c, next);
                      }
                    },
                    child: Text(e.value),
                  ),
              ],
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () =>
                showData(c, '实体原始详情', row, export: widget.exportText),
            child: const Text('复制 / 导出'),
          ),
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: const Text('关闭'),
          ),
        ],
      ),
    );
    if (prepared != null && mounted) {
      widget.onPrepare(prepared);
      Navigator.pop(context);
    }
  }

  List<Widget> entityDetails(BuildContext context, JsonMap row) {
    final parts = kafkaValue(row, 'partitions'),
        members = kafkaValue(row, 'members');
    return [
      if (parts is Map) ...[
        const Text('分区详情'),
        for (final e in parts.entries)
          ListTile(
            title: Text('分区 ${e.key}'),
            subtitle: Text(
              'Leader ${kafkaValue(mapOf(e.value), 'leader') ?? '—'} · 副本 ${kafkaValue(mapOf(e.value), 'replicas') ?? '—'} · ISR ${kafkaValue(mapOf(e.value), 'isr') ?? '—'}',
            ),
            trailing: const Icon(Icons.read_more),
            onTap: () {
              final next = kafkaDraft(widget.request, 'consume', row);
              next['params'] = {...mapOf(next['params']), 'partition': e.key};
              Navigator.pop(context, next);
            },
          ),
      ],
      if (members is List) ...[
        const Text('消费组成员'),
        for (final item in members)
          ListTile(
            title: Text(
              '${kafkaValue(mapOf(item), 'memberid') ?? kafkaValue(mapOf(item), 'clientid') ?? '成员'}',
            ),
            subtitle: Text(
              '${kafkaValue(mapOf(item), 'clienthost') ?? kafkaValue(mapOf(item), 'host') ?? ''}',
            ),
          ),
      ],
      if (row['schema'] != null) ...[
        const Text('Schema 定义'),
        SelectableText(row['schema'].toString()),
      ],
      if (row['status'] != null) ...[
        const Text('Connector 状态'),
        SelectableText(pretty(row['status'])),
      ],
      if (row['tasks'] is List) ...[
        const Text('任务'),
        for (final task in row['tasks'] as List)
          ListTile(
            title: Text('任务 ${mapOf(task)['id']}'),
            subtitle: Text('${mapOf(task)['state']}'),
          ),
      ],
    ];
  }
}
