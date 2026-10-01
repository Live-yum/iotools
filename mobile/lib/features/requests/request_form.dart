import 'package:flutter/material.dart';
import '../../core/json.dart';
import '../../core/session.dart';
import '../../shared/widgets.dart';
import '../http/crypto_editor.dart';

class RequestForm extends StatefulWidget {
  const RequestForm({
    required this.session,
    required this.request,
    required this.onChanged,
    super.key,
  });
  final AppSession session;
  final JsonMap request;
  final ValueChanged<JsonMap> onChanged;
  @override
  State<RequestForm> createState() => _RequestFormState();
}

class _RequestFormState extends State<RequestForm> {
  late JsonMap request;
  final Map<String, TextEditingController> inputs = {};
  @override
  void initState() {
    super.initState();
    request = cloneMap(widget.request);
  }

  @override
  void dispose() {
    for (final c in inputs.values) {
      c.dispose();
    }
    super.dispose();
  }

  JsonMap get params => mapOf(request['params']);
  void change(String key, Object? value) {
    final p = params;
    if (value == null)
      p.remove(key);
    else
      p[key] = value;
    request['params'] = p;
    widget.onChanged(cloneMap(request));
  }

  void root(String key, String value) {
    request[key] = value;
    widget.onChanged(cloneMap(request));
  }

  TextEditingController input(String key, Object? value) => inputs.putIfAbsent(
    key,
    () => TextEditingController(text: value?.toString() ?? ''),
  );
  @override
  Widget build(BuildContext context) {
    final protocols = rowsOf(widget.session.catalog['protocols']);
    final protocol =
        protocols.where((p) => p['id'] == request['protocol']).firstOrNull ??
        {};
    final actions = rowsOf(protocol['actions']);
    final fields = rowsOf(protocol['fields']);
    final core = fields
        .where(
          (f) => _primary(request['protocol'].toString(), f['key'].toString()),
        )
        .toList();
    final advanced = fields
        .where(
          (f) =>
              !core.contains(f) &&
              ![
                'crypto',
                'request_transforms',
                'response_transform',
              ].contains(f['key']),
        )
        .toList();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Section(
          '请求',
          children: [
            TextField(
              key: const ValueKey('request_name'),
              controller: input('name', request['name']),
              decoration: const InputDecoration(labelText: '名称'),
              onChanged: (v) => root('name', v),
            ),
            const SizedBox(height: 12),
            Row(
              children: [
                Expanded(
                  child: ChoiceField(
                    label: '协议',
                    value: request['protocol']?.toString() ?? 'http',
                    options: protocols.map((p) => p['id'].toString()).toList(),
                    onChanged: (v) {
                      final target = protocols.firstWhere((p) => p['id'] == v);
                      final a = rowsOf(target['actions']).first;
                      setState(() {
                        request['protocol'] = v;
                        request['action'] = a['id'];
                        request['params'] = cloneMap(mapOf(a['defaults']));
                        inputs.clear();
                      });
                      widget.onChanged(cloneMap(request));
                    },
                  ),
                ),
                const SizedBox(width: 10),
                Expanded(
                  child: DropdownButtonFormField<String>(
                    key: ValueKey(
                      '${request['protocol']}/${request['action']}',
                    ),
                    initialValue: request['action']?.toString(),
                    isExpanded: true,
                    decoration: const InputDecoration(labelText: '操作'),
                    items: actions
                        .map(
                          (a) => DropdownMenuItem(
                            value: a['id'].toString(),
                            child: Text(
                              a['name'].toString(),
                              overflow: TextOverflow.ellipsis,
                            ),
                          ),
                        )
                        .toList(),
                    onChanged: (v) {
                      if (v == null) return;
                      setState(() {
                        request['action'] = v;
                        request['params'] = {
                          ...mapOf(
                            actions.firstWhere((a) => a['id'] == v)['defaults'],
                          ),
                          ...params,
                        };
                      });
                      widget.onChanged(cloneMap(request));
                    },
                  ),
                ),
              ],
            ),
            const SizedBox(height: 12),
            TextField(
              key: const ValueKey('request_endpoint'),
              controller: input('endpoint', request['endpoint']),
              decoration: const InputDecoration(
                labelText: '连接地址',
                hintText: '例如 https://api.example.com',
              ),
              keyboardType: TextInputType.url,
              onChanged: (v) => root('endpoint', v),
            ),
            const SizedBox(height: 12),
            TextField(
              controller: input('timeout', request['timeout']),
              decoration: const InputDecoration(labelText: '超时（例如 10s）'),
              onChanged: (v) => root('timeout', v),
            ),
          ],
        ),
        Section('请求参数', children: core.map(field).toList()),
        if (request['protocol'] == 'http') ...[
          CryptoDefinitions(
            value: mapOf(params['crypto']),
            onChanged: (v) => setState(() => change('crypto', v)),
          ),
          TransformEditor(
            value: rowsOf(params['request_transforms']),
            codecs: mapOf(params['crypto']).keys.toList(),
            onChanged: (v) => setState(() => change('request_transforms', v)),
          ),
          TransformEditor(
            response: true,
            value: rowsOf(params['response_transform']),
            codecs: mapOf(params['crypto']).keys.toList(),
            onChanged: (v) => setState(() => change('response_transform', v)),
          ),
        ],
        Card(
          child: ExpansionTile(
            title: const Text('连接、安全与高级参数'),
            subtitle: const Text('认证、TLS、协议限制与全部可选字段'),
            children: [
              Padding(
                padding: const EdgeInsets.all(16),
                child: Column(children: advanced.map(field).toList()),
              ),
            ],
          ),
        ),
        const SizedBox(height: 16),
      ],
    );
  }

  Widget field(JsonMap spec) {
    final key = spec['key'].toString(),
        type = spec['type'].toString(),
        label = spec['label'].toString();
    final value = params[key];
    if (type == 'boolean')
      return SwitchListTile(
        title: Text(label),
        value: value == true,
        onChanged: (v) => setState(() => change(key, v)),
      );
    if (type == 'json')
      return Padding(
        padding: const EdgeInsets.only(bottom: 12),
        child: OutlinedButton(
          onPressed: () async {
            final x = await editStructured(context, label, value, key);
            if (x != null) setState(() => change(key, x));
          },
          child: Align(
            alignment: Alignment.centerLeft,
            child: Text(
              '$label${value == null ? '' : ' · ${value is Map
                        ? value.length
                        : value is List
                        ? value.length
                        : '已设置'}'}',
            ),
          ),
        ),
      );
    final choices = switch (key) {
      'qos' => ['0', '1', '2'],
      'payload_encoding' => ['text', 'hex', 'base64'],
      'word_order' => ['ABCD', 'CDAB', 'BADC', 'DCBA'],
      'sasl' => ['plain', 'scram-sha-256', 'scram-sha-512'],
      'key_format' || 'value_format' => ['text', 'json', 'avro'],
      'offset' => ['latest', 'earliest'],
      _ => <String>[],
    };
    if (choices.isNotEmpty && key != 'offset')
      return Padding(
        padding: const EdgeInsets.only(bottom: 12),
        child: ChoiceField(
          label: label,
          value: value?.toString() ?? choices.first,
          options: choices,
          onChanged: (v) => change(key, v),
        ),
      );
    final attachment = key.endsWith('_file') || key == 'next_config';
    return Padding(
      padding: const EdgeInsets.only(bottom: 12),
      child: TextField(
        key: ValueKey('param_$key'),
        controller: input(key, value),
        obscureText: type == 'password',
        minLines: ['body', 'payload', 'value'].contains(key) ? 3 : 1,
        maxLines: type == 'password'
            ? 1
            : ['body', 'payload', 'value'].contains(key)
            ? 8
            : 1,
        keyboardType: type == 'number'
            ? const TextInputType.numberWithOptions(signed: true, decimal: true)
            : TextInputType.text,
        decoration: InputDecoration(
          labelText: label,
          helperText: spec['hint']?.toString().isNotEmpty == true
              ? spec['hint'].toString()
              : null,
          suffixIcon: attachment
              ? IconButton(
                  tooltip: '选择并导入私有附件',
                  icon: const Icon(Icons.attach_file),
                  onPressed: () => guarded(context, () async {
                    final f = mapOf(
                      await widget.session.platform.invoke('files.pick'),
                    );
                    if (f.isNotEmpty) {
                      inputs[key]!.text = f['path'].toString();
                      change(key, f['path']);
                    }
                  }),
                )
              : null,
        ),
        onChanged: (v) {
          if (v.isEmpty) {
            change(key, null);
          } else {
            change(key, v);
          }
        },
      ),
    );
  }
}

bool _primary(String protocol, String key) => switch (protocol) {
  'http' => [
    'headers',
    'query',
    'body',
    'json',
    'form_urlencoded',
    'form_multipart',
    'bearer',
    'username',
    'password',
  ].contains(key),
  'mqtt' => [
    'topic',
    'topics',
    'qos',
    'payload',
    'payload_encoding',
    'retain',
    'limit',
  ].contains(key),
  'kafka' => [
    'topic',
    'group',
    'subject',
    'version',
    'connector',
    'partition',
    'offset',
    'limit',
    'key',
    'value',
    'partitions',
    'replication_factor',
    'configs',
    'headers',
    'json',
  ].contains(key),
  'modbus' => [
    'unit',
    'address',
    'count',
    'value',
    'values',
    'value_type',
    'word_order',
    'read_address',
    'read_count',
  ].contains(key),
  'opcua' => [
    'node_id',
    'object_id',
    'method_id',
    'value',
    'value_type',
    'arguments',
    'node_ids',
  ].contains(key),
  _ => false,
};

Future<Object?> editStructured(
  BuildContext context,
  String title,
  Object? initial,
  String key,
) async {
  if (key == 'value' || key == 'json') {
    final raw = await inputDialog(
      context,
      title,
      initial: initial == null ? '' : exactEncode(initial),
      hint: 'JSON 值；整数保留原始精度',
      multiline: true,
    );
    return raw == null ? null : exactDecode(raw);
  }
  final listKeys = [
    'topics',
    'confirm_topics',
    'groups',
    'consume_partitions',
    'node_ids',
    'attributes',
    'redirect_origins',
    'ignore_certificate_hosts',
    'values',
    'units',
    'pins',
    'arguments',
  ];
  final isList = initial is List || listKeys.contains(key);
  final entries = <MapEntry<String, Object?>>[
    if (initial is Map)
      ...initial.entries.map((e) => MapEntry(e.key.toString(), e.value)),
    if (initial is List)
      ...initial.asMap().entries.map(
        (e) => MapEntry(e.key.toString(), e.value),
      ),
  ];
  return memoryDialog<Object>(
    context: context,
    builder: (c) => StatefulBuilder(
      builder: (c, set) => AlertDialog(
        title: Text(title),
        content: SizedBox(
          width: 640,
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                ...entries.asMap().entries.map(
                  (row) => ListTile(
                    title: Text(isList ? '${row.key + 1}' : row.value.key),
                    subtitle: Text(
                      row.value.value?.toString() ?? 'null',
                      maxLines: 3,
                      overflow: TextOverflow.ellipsis,
                    ),
                    onTap: () async {
                      final v = await inputDialog(
                        c,
                        '值',
                        initial: row.value.value is String
                            ? row.value.value as String
                            : exactEncode(row.value.value),
                        multiline: true,
                      );
                      if (v != null)
                        set(
                          () => entries[row.key] = MapEntry(
                            row.value.key,
                            _fieldValue(v, key),
                          ),
                        );
                    },
                    trailing: IconButton(
                      tooltip: '删除字段',
                      icon: const Icon(Icons.close),
                      onPressed: () => set(() => entries.removeAt(row.key)),
                    ),
                  ),
                ),
                OutlinedButton.icon(
                  onPressed: () async {
                    String? name = isList
                        ? '${entries.length}'
                        : await inputDialog(c, '名称');
                    if (name == null || name.isEmpty || !c.mounted) return;
                    final value = await inputDialog(c, '值', multiline: true);
                    if (value != null)
                      set(
                        () => entries.add(
                          MapEntry(name, _fieldValue(value, key)),
                        ),
                      );
                  },
                  icon: const Icon(Icons.add),
                  label: Text(isList ? '添加项目' : '添加键值'),
                ),
              ],
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: const Text('取消'),
          ),
          TextButton(
            onPressed: () async {
              final raw = await inputDialog(
                c,
                '高级 JSON 编辑',
                initial: exactEncode(
                  isList
                      ? entries.map((e) => e.value).toList()
                      : Map.fromEntries(entries),
                ),
                multiline: true,
              );
              if (raw != null && c.mounted) {
                guarded(c, () async {
                  Navigator.pop(c, exactDecode(raw));
                });
              }
            },
            child: const Text('高级 JSON'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(
              c,
              isList
                  ? entries.map((e) => e.value).toList()
                  : Map.fromEntries(entries),
            ),
            child: const Text('应用'),
          ),
        ],
      ),
    ),
  );
}

Object? _fieldValue(String text, String key) {
  if ([
    'values',
    'units',
    'pins',
    'consume_partitions',
    'arguments',
  ].contains(key)) {
    try {
      return exactDecode(text);
    } catch (_) {
      return text;
    }
  }
  return text;
}
