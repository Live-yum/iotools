import 'package:flutter/material.dart';
import '../../core/json.dart';
import '../../shared/widgets.dart';

const algorithms = [
  'none',
  'base64',
  'base64url',
  'aes-128-cbc',
  'aes-192-cbc',
  'aes-256-cbc',
  'aes-128-ecb',
  'aes-192-ecb',
  'aes-256-ecb',
];
JsonMap buildCodec(
  String algorithm, {
  String key = '',
  String keyEncoding = 'text',
  String iv = '',
  String ivEncoding = 'text',
  String ciphertext = 'base64',
  bool whitespace = false,
  bool missingPadding = false,
}) {
  if (!algorithms.contains(algorithm)) throw const FormatException('不支持的编码器');
  final result = <String, dynamic>{'algorithm': algorithm};
  if (algorithm.startsWith('aes')) {
    result['key'] = {'value': key, 'encoding': keyEncoding};
    if (algorithm.endsWith('cbc'))
      result['iv'] = {'value': iv, 'encoding': ivEncoding};
    result['ciphertext_encoding'] = ciphertext;
  }
  if (algorithm == 'base64' || algorithm == 'base64url') {
    result['base64_decode'] = {
      'ignore_ascii_whitespace': whitespace,
      'allow_missing_padding': missingPadding,
    };
  }
  return result;
}

Future<JsonMap?> editCodec(
  BuildContext context, [
  JsonMap existing = const {},
]) => memoryDialog<JsonMap>(
  context: context,
  builder: (_) => CodecDialog(existing: existing),
);

class CodecDialog extends StatefulWidget {
  const CodecDialog({this.existing = const {}, super.key});
  final JsonMap existing;
  @override
  State<CodecDialog> createState() => _CodecDialogState();
}

class _CodecDialogState extends State<CodecDialog> {
  late String algorithm, keyEncoding, ivEncoding, ciphertext;
  late TextEditingController keyText, ivText;
  late bool whitespace, missingPadding;
  @override
  void initState() {
    super.initState();
    final e = widget.existing;
    algorithm = e['algorithm']?.toString() ?? 'base64';
    keyEncoding = mapOf(e['key'])['encoding']?.toString() ?? 'text';
    ivEncoding = mapOf(e['iv'])['encoding']?.toString() ?? 'text';
    ciphertext = e['ciphertext_encoding']?.toString() ?? 'base64';
    keyText = TextEditingController(
      text: mapOf(e['key'])['value']?.toString() ?? '',
    );
    ivText = TextEditingController(
      text: mapOf(e['iv'])['value']?.toString() ?? '',
    );
    whitespace = mapOf(e['base64_decode'])['ignore_ascii_whitespace'] == true;
    missingPadding = mapOf(e['base64_decode'])['allow_missing_padding'] == true;
  }

  @override
  void dispose() {
    keyText.dispose();
    ivText.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('编码与加密定义'),
    content: SizedBox(
      width: 540,
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ChoiceField(
              key: const ValueKey('codec_algorithm'),
              label: '算法',
              value: algorithm,
              options: algorithms,
              onChanged: (v) => setState(() => algorithm = v),
            ),
            if (algorithm.startsWith('aes')) ...[
              const SizedBox(height: 12),
              TextField(
                key: const ValueKey('codec_key'),
                controller: keyText,
                obscureText: true,
                decoration: const InputDecoration(labelText: '密钥'),
              ),
              ChoiceField(
                label: '密钥编码',
                value: keyEncoding,
                options: const ['text', 'hex', 'base64'],
                onChanged: (v) => setState(() => keyEncoding = v),
              ),
              if (algorithm.endsWith('cbc')) ...[
                TextField(
                  key: const ValueKey('codec_iv'),
                  controller: ivText,
                  obscureText: true,
                  decoration: const InputDecoration(labelText: 'IV（16 字节）'),
                ),
                ChoiceField(
                  label: 'IV 编码',
                  value: ivEncoding,
                  options: const ['text', 'hex', 'base64'],
                  onChanged: (v) => setState(() => ivEncoding = v),
                ),
              ],
              ChoiceField(
                label: '密文编码',
                value: ciphertext,
                options: const ['base64', 'base64url'],
                onChanged: (v) => setState(() => ciphertext = v),
              ),
              const Text('PKCS7 填充 · UTF-8 明文。保存前由共享内核校验密钥长度。'),
            ],
            if (algorithm == 'base64' || algorithm == 'base64url') ...[
              SwitchListTile(
                title: const Text('解码忽略 ASCII 空白'),
                value: whitespace,
                onChanged: (v) => setState(() => whitespace = v),
              ),
              SwitchListTile(
                title: const Text('允许缺少末尾填充'),
                value: missingPadding,
                onChanged: (v) => setState(() => missingPadding = v),
              ),
            ],
          ],
        ),
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('取消'),
      ),
      FilledButton(
        key: const ValueKey('codec_apply'),
        onPressed: () => Navigator.pop(
          context,
          buildCodec(
            algorithm,
            key: keyText.text,
            keyEncoding: keyEncoding,
            iv: ivText.text,
            ivEncoding: ivEncoding,
            ciphertext: ciphertext,
            whitespace: whitespace,
            missingPadding: missingPadding,
          ),
        ),
        child: const Text('应用'),
      ),
    ],
  );
}

class CryptoDefinitions extends StatefulWidget {
  const CryptoDefinitions({
    required this.value,
    required this.onChanged,
    super.key,
  });
  final JsonMap value;
  final ValueChanged<JsonMap> onChanged;
  @override
  State<CryptoDefinitions> createState() => _CryptoDefinitionsState();
}

class _CryptoDefinitionsState extends State<CryptoDefinitions> {
  @override
  Widget build(BuildContext context) => Section(
    '编码 / 加密定义',
    subtitle: '密钥仅保存在当前草稿；点击保存请求才会写入配置',
    children: [
      ...widget.value.entries.map(
        (e) => ListTile(
          title: Text(e.key),
          subtitle: Text(mapOf(e.value)['algorithm']?.toString() ?? ''),
          trailing: Wrap(
            children: [
              IconButton(
                tooltip: '编辑',
                icon: const Icon(Icons.edit_outlined),
                onPressed: () async {
                  final v = await editCodec(context, mapOf(e.value));
                  if (v != null) widget.onChanged({...widget.value, e.key: v});
                },
              ),
              IconButton(
                tooltip: '删除',
                icon: const Icon(Icons.delete_outline),
                onPressed: () {
                  final v = {...widget.value}..remove(e.key);
                  widget.onChanged(v);
                },
              ),
            ],
          ),
        ),
      ),
      OutlinedButton.icon(
        key: const ValueKey('add_codec'),
        onPressed: () async {
          final id = await inputDialog(context, '定义名称', hint: '例如 aes_request');
          if (id == null || id.trim().isEmpty || !context.mounted) return;
          final v = await editCodec(context);
          if (v != null) widget.onChanged({...widget.value, id.trim(): v});
        },
        icon: const Icon(Icons.add),
        label: const Text('添加编码 / 加密'),
      ),
    ],
  );
}

class TransformEditor extends StatelessWidget {
  const TransformEditor({
    required this.value,
    required this.onChanged,
    required this.codecs,
    this.response = false,
    super.key,
  });
  final List<JsonMap> value;
  final ValueChanged<List<JsonMap>> onChanged;
  final List<String> codecs;
  final bool response;
  @override
  Widget build(BuildContext context) => Section(
    response ? '响应转换顺序' : '请求转换顺序',
    subtitle: '按列表顺序执行；移动按钮改变实际字节处理顺序',
    children: [
      ...value.asMap().entries.map(
        (e) => ListTile(
          title: Text(
            '${e.key + 1}. ${e.value['type']} · ${e.value['crypto'] ?? ''}',
          ),
          subtitle: Text(
            '${e.value['scope'] ?? e.value['target'] ?? ''} ${e.value['name'] ?? e.value['paths'] ?? ''}',
          ),
          trailing: Wrap(
            children: [
              IconButton(
                tooltip: '上移',
                onPressed: e.key == 0
                    ? null
                    : () {
                        final v = [...value];
                        final x = v.removeAt(e.key);
                        v.insert(e.key - 1, x);
                        onChanged(v);
                      },
                icon: const Icon(Icons.arrow_upward),
              ),
              IconButton(
                tooltip: '编辑转换',
                onPressed: () async {
                  final x = await editTransform(
                    context,
                    e.value,
                    response: response,
                    codecs: codecs,
                  );
                  if (x != null) {
                    final v = [...value];
                    v[e.key] = x;
                    onChanged(v);
                  }
                },
                icon: const Icon(Icons.edit_outlined),
              ),
              IconButton(
                tooltip: '删除转换',
                onPressed: () {
                  final v = [...value]..removeAt(e.key);
                  onChanged(v);
                },
                icon: const Icon(Icons.close),
              ),
            ],
          ),
        ),
      ),
      OutlinedButton.icon(
        key: ValueKey(
          response ? 'add_response_transform' : 'add_request_transform',
        ),
        onPressed: () async {
          final x = await editTransform(
            context,
            {},
            response: response,
            codecs: codecs,
          );
          if (x != null) onChanged([...value, x]);
        },
        icon: const Icon(Icons.add),
        label: const Text('添加转换步骤'),
      ),
    ],
  );
}

Future<JsonMap?> editTransform(
  BuildContext context,
  JsonMap initial, {
  required bool response,
  required List<String> codecs,
}) async {
  String type = initial['type']?.toString() ?? 'encode',
      scope = initial[response ? 'scope' : 'target']?.toString() ?? 'body',
      codec =
          initial['crypto']?.toString() ?? (codecs.isEmpty ? '' : codecs.first),
      encoding = initial['text_encoding']?.toString() ?? 'utf8';
  final name = TextEditingController(text: initial['name']?.toString() ?? ''),
      paths = TextEditingController(
        text: (initial['paths'] as List? ?? []).join('\n'),
      );
  bool missing = initial['skip_missing'] == true,
      nulls = initial['skip_null'] == true,
      blank = initial['skip_blank'] == true;
  final result = await memoryDialog<JsonMap>(
    context: context,
    builder: (c) => StatefulBuilder(
      builder: (c, set) => AlertDialog(
        title: Text(response ? '响应转换' : '请求转换'),
        content: SizedBox(
          width: 540,
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                ChoiceField(
                  key: const ValueKey('transform_type'),
                  label: '操作',
                  value: type,
                  options: [
                    'encode',
                    'decode',
                    'encrypt',
                    'decrypt',
                    if (response) 'parse_json',
                  ],
                  onChanged: (v) => set(() => type = v),
                ),
                ChoiceField(
                  key: const ValueKey('transform_scope'),
                  label: response ? '范围' : '目标',
                  value: scope,
                  options: response
                      ? ['body', 'fields']
                      : ['body', 'header', 'query', 'json'],
                  onChanged: (v) => set(() => scope = v),
                ),
                if (type != 'parse_json')
                  ChoiceField(
                    key: const ValueKey('transform_codec'),
                    label: '编码定义',
                    value: codec,
                    options: codecs,
                    onChanged: (v) => codec = v,
                  ),
                if (!response && scope != 'body')
                  TextField(
                    controller: name,
                    decoration: InputDecoration(
                      labelText: scope == 'json' ? 'JSONPath' : '字段名称',
                    ),
                  ),
                if (response && scope == 'fields') ...[
                  TextField(
                    controller: paths,
                    minLines: 2,
                    maxLines: 5,
                    decoration: const InputDecoration(
                      labelText: 'JSONPath（每行一个）',
                    ),
                  ),
                  CheckboxListTile(
                    title: const Text('跳过缺失字段'),
                    value: missing,
                    onChanged: (v) => set(() => missing = v!),
                  ),
                  CheckboxListTile(
                    title: const Text('跳过 null'),
                    value: nulls,
                    onChanged: (v) => set(() => nulls = v!),
                  ),
                  CheckboxListTile(
                    title: const Text('跳过空白值'),
                    value: blank,
                    onChanged: (v) => set(() => blank = v!),
                  ),
                ],
                if (response && scope == 'body') ...[
                  ChoiceField(
                    label: '文本编码',
                    value: encoding,
                    options: const ['utf8', 'utf8-sig'],
                    onChanged: (v) => set(() => encoding = v),
                  ),
                  const Text('正文转换须为第一步；转换后解析 JSON，再继续字段转换'),
                ],
              ],
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: const Text('取消'),
          ),
          FilledButton(
            key: const ValueKey('transform_apply'),
            onPressed: () {
              if (type != 'parse_json' && codec.isEmpty) {
                reportError(c, '请先添加编码定义');
                return;
              }
              if (type == 'parse_json' && scope != 'fields') {
                reportError(c, '解析 JSON 适用于字段范围');
                return;
              }
              Navigator.pop(c, <String, dynamic>{
                'type': type,
                if (type != 'parse_json') 'crypto': codec,
                response ? 'scope' : 'target': scope,
                if (!response && scope != 'body') 'name': name.text,
                if (response && scope == 'fields')
                  ...'paths skip_missing skip_null skip_blank'
                      .split(' ')
                      .asMap()
                      .map(
                        (i, k) => MapEntry(
                          k,
                          [
                            paths.text
                                .split('\n')
                                .where((s) => s.trim().isNotEmpty)
                                .toList(),
                            missing,
                            nulls,
                            blank,
                          ][i],
                        ),
                      ),
                if (response && scope == 'body')
                  ...'text_encoding'
                      .split(' ')
                      .asMap()
                      .map((i, k) => MapEntry(k, encoding)),
                if (response && scope == 'body') 'parse': 'json',
              });
            },
            child: const Text('应用'),
          ),
        ],
      ),
    ),
  );
  name.dispose();
  paths.dispose();
  return result;
}
