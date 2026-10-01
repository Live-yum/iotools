import 'package:flutter/material.dart';
import '../../core/json.dart';
import '../../core/session.dart';
import '../../shared/widgets.dart';
import 'crypto_editor.dart';

class HttpTools extends StatelessWidget {
  const HttpTools({required this.session, required this.onRun, super.key});
  final AppSession session;
  final Future<void> Function(JsonMap, JsonMap?) onRun;
  @override
  Widget build(BuildContext context) => Column(
    children: [
      Section(
        'HTTP 工具',
        children: [
          OutlinedButton(
            onPressed: () => guarded(context, () async {
              final r = await session.command({
                'op': 'http.curl',
                'request': session.draft,
              });
              if (context.mounted)
                showData(
                  context,
                  'cURL · 可能包含凭据',
                  mapOf(r)['curl'],
                  export: session.exportText,
                );
            }),
            child: const Text('生成 cURL'),
          ),
          OutlinedButton(
            onPressed: () => guarded(context, () async {
              if (await confirm(
                    context,
                    '执行依赖并生成 cURL？',
                    '将运行模板依赖请求；依赖写操作仍会逐项确认。生成的文本不会自动执行。',
                  ) &&
                  context.mounted) {
                session.started(
                  mapOf(
                    await session.command({
                      'op': 'http.curl',
                      'request': session.draft,
                      'execute_triggers': true,
                      'confirmed': true,
                    }),
                  ),
                );
              }
            }),
            child: const Text('执行依赖后生成 cURL'),
          ),
          OutlinedButton(
            onPressed: () => overrides(context),
            child: const Text('单次变量 / 请求覆盖'),
          ),
          OutlinedButton(
            onPressed: () => filter(context),
            child: const Text('jq 响应筛选（可取消）'),
          ),
        ],
      ),
      Section(
        '本地编码与加密',
        children: [
          OutlinedButton(
            onPressed: () => Navigator.push(
              context,
              MaterialPageRoute(builder: (_) => CryptoLab(session: session)),
            ),
            child: const Text('编码 / 加密实验室'),
          ),
        ],
      ),
    ],
  );
  Future<void> overrides(BuildContext context) async {
    final fields = TextEditingController(),
        headers = TextEditingController(),
        query = TextEditingController(),
        form = TextEditingController(),
        url = TextEditingController(),
        body = TextEditingController(),
        bearer = TextEditingController(),
        basic = TextEditingController();
    final controllers = [
      fields,
      headers,
      query,
      form,
      url,
      body,
      bearer,
      basic,
    ];
    final names = [
      '环境变量（name=value，每行一个）',
      '请求头（name=value；仅 name 表示删除）',
      '查询参数（重复 name 保留多值）',
      '表单覆盖（name=value）',
      '单次 URL',
      '单次请求体',
      '单次 Bearer',
      '单次 Basic（user:password）',
    ];
    final r = await memoryDialog<JsonMap>(
      context: context,
      builder: (c) => AlertDialog(
        title: const Text('仅本次执行'),
        content: SizedBox(
          width: 600,
          child: SingleChildScrollView(
            child: Column(
              children: List.generate(
                controllers.length,
                (i) => Padding(
                  padding: const EdgeInsets.only(bottom: 12),
                  child: TextField(
                    controller: controllers[i],
                    obscureText: i >= 6,
                    minLines: i < 4 ? 2 : 1,
                    maxLines: i < 4 ? 5 : 1,
                    decoration: InputDecoration(labelText: names[i]),
                  ),
                ),
              ),
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: () {
              try {
                Navigator.pop(
                  c,
                  temporaryOverrides(
                    fields: fields.text,
                    headers: headers.text,
                    query: query.text,
                    form: form.text,
                    url: url.text,
                    body: body.text,
                    bearer: bearer.text,
                    basic: basic.text,
                  ),
                );
              } catch (e) {
                reportError(c, e);
              }
            },
            child: const Text('预览本次执行'),
          ),
        ],
      ),
    );
    for (final c in controllers) {
      c.dispose();
    }
    if (r != null) await onRun(session.draft, r);
  }

  Future<void> filter(BuildContext context) async {
    final response = session.events.reversed
        .where((e) => ['response', 'transformed'].contains(e['kind']))
        .firstOrNull;
    final data = mapOf(response?['data']);
    final query = await inputDialog(
      context,
      'jq 筛选表达式',
      initial: '.',
      hint: '.items[] | select(.active)',
    );
    if (query == null || !context.mounted) return;
    await guarded(context, () async {
      session.started(
        mapOf(
          await session.command({
            'op': 'http.filter.start',
            'query': query,
            'data': data['body'] ?? data,
          }),
        ),
      );
    });
  }
}

JsonMap temporaryOverrides({
  String fields = '',
  String headers = '',
  String query = '',
  String form = '',
  String url = '',
  String body = '',
  String bearer = '',
  String basic = '',
}) {
  final result = <String, dynamic>{};
  for (final e in {
    'fields': fields,
    'headers': headers,
    'query': query,
    'form': form,
  }.entries) {
    final lines = e.value.split('\n').where((l) => l.isNotEmpty).toList();
    if (e.key == 'fields' &&
        lines.any((l) => !l.contains('=') || l.startsWith('=')))
      throw const FormatException('变量必须使用 name=value');
    if (lines.isNotEmpty) result[e.key] = lines;
  }
  for (final e in {
    'url': url,
    'body': body,
    'bearer': bearer,
    'basic': basic,
  }.entries) {
    if (e.value.isNotEmpty) result[e.key] = e.value;
  }
  if (bearer.isNotEmpty && basic.isNotEmpty)
    throw const FormatException('Bearer 和 Basic 不能同时覆盖');
  return result;
}

class CryptoLab extends StatefulWidget {
  const CryptoLab({required this.session, super.key});
  final AppSession session;
  @override
  State<CryptoLab> createState() => _CryptoLabState();
}

class _CryptoLabState extends State<CryptoLab> {
  JsonMap codec = {'algorithm': 'base64'}, codecs = {};
  List<JsonMap> rules = [];
  String direction = 'encode';
  Object? output;
  bool batch = false, busy = false;
  final data = TextEditingController();
  @override
  void dispose() {
    data.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('编码 / 加密实验室')),
    body: ListView(
      padding: const EdgeInsets.all(16),
      children: [
        SwitchListTile(
          title: const Text('JSONPath 批量转换'),
          value: batch,
          onChanged: (v) => setState(() => batch = v),
        ),
        TextField(
          key: const ValueKey('crypto_data'),
          controller: data,
          minLines: 5,
          maxLines: 12,
          decoration: const InputDecoration(labelText: '输入数据（仅内存）'),
        ),
        if (!batch)
          Section(
            '转换设置',
            children: [
              ChoiceField(
                label: '方向',
                value: direction,
                options: const ['encode', 'decode'],
                onChanged: (v) => direction = v,
              ),
              OutlinedButton(
                onPressed: () async {
                  final value = await editCodec(context, codec);
                  if (value != null) setState(() => codec = value);
                },
                child: Text('算法：${codec['algorithm']}'),
              ),
            ],
          )
        else ...[
          CryptoDefinitions(
            value: codecs,
            onChanged: (v) => setState(() => codecs = v),
          ),
          TransformEditor(
            value: rules,
            response: true,
            codecs: codecs.keys.toList(),
            onChanged: (v) => setState(() => rules = v),
          ),
        ],
        FilledButton(
          onPressed: busy
              ? null
              : () => guarded(context, () async {
                  setState(() => busy = true);
                  try {
                    final r = await widget.session.command(
                      batch
                          ? {
                              'op': 'crypto.transform',
                              'data': data.text,
                              'codecs': codecs,
                              'rules': rules,
                            }
                          : {
                              'op': 'crypto.convert',
                              'data': data.text,
                              'direction': direction,
                              'codec': codec,
                            },
                    );
                    if (mounted) setState(() => output = r);
                  } finally {
                    if (mounted) setState(() => busy = false);
                  }
                }),
          child: Text(busy ? '正在转换' : '转换'),
        ),
        if (output != null)
          Section(
            '转换结果',
            children: [
              DataView(output),
              OutlinedButton(
                onPressed: () => showData(
                  context,
                  '完整转换结果',
                  output,
                  export: widget.session.exportText,
                ),
                child: const Text('复制 / 导出'),
              ),
            ],
          ),
      ],
    ),
  );
}
