import 'package:flutter/material.dart';
import 'opcua_models.dart';

/// Typed controls, including structured fields and ordered one-dimensional arrays.
/// No JSON textarea participates in write or method argument entry.
class UaTypedEditor extends StatefulWidget {
  const UaTypedEditor({
    super.key,
    required this.initialType,
    this.initialValue,
    this.chooseType = false,
    required this.onChanged,
    this.fieldKey = 'ua-value',
  });
  final String initialType, fieldKey;
  final dynamic initialValue;
  final bool chooseType;
  final ValueChanged<UaMap> onChanged;
  @override
  State<UaTypedEditor> createState() => _UaTypedEditorState();
}

class _UaTypedEditorState extends State<UaTypedEditor>
    with WidgetsBindingObserver {
  Route<dynamic>? _elementRoute;
  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if ((state == AppLifecycleState.hidden ||
            state == AppLifecycleState.paused ||
            state == AppLifecycleState.detached) &&
        _elementRoute?.isActive == true)
      _elementRoute?.navigator?.removeRoute(_elementRoute!);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  late String type;
  late dynamic raw;
  final typedDrafts = <String, dynamic>{};
  int page = 0;
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    type = uaType(widget.initialType);
    if (!uaTypes.contains(type.replaceAll('[]', ''))) type = 'String';
    try {
      raw = uaEditable(type, widget.initialValue);
    } on FormatException {
      raw = widget.initialValue;
    }
  }

  void change(dynamic value) {
    raw = value;
    widget.onChanged({'type': type, 'value': raw});
    setState(() {});
  }

  void changeType(String next) {
    typedDrafts[type] = raw;
    type = next;
    raw = typedDrafts[next] ?? uaDefault(next);
    page = 0;
    widget.onChanged({'type': type, 'value': raw});
    setState(() {});
  }

  Widget field(
    String label,
    String value,
    ValueChanged<String> change, {
    String suffix = '',
    bool multi = false,
  }) => Padding(
    padding: const EdgeInsets.only(bottom: 12),
    child: TextFormField(
      key: ValueKey('${widget.fieldKey}-$type$suffix'),
      initialValue: value,
      restorationId: null,
      decoration: InputDecoration(labelText: label),
      maxLines: multi ? 3 : 1,
      onChanged: change,
      autocorrect: false,
      enableSuggestions: false,
    ),
  );
  @override
  Widget build(BuildContext context) {
    final base = type.replaceAll('[]', ''), array = type.endsWith('[]');
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        if (widget.chooseType) ...[
          DropdownButtonFormField<String>(
            isExpanded: true,
            key: ValueKey('${widget.fieldKey}-type'),
            initialValue: base,
            decoration: const InputDecoration(labelText: 'OPC UA 类型'),
            items: uaTypes
                .map((t) => DropdownMenuItem(value: t, child: Text(t)))
                .toList(),
            onChanged: (t) {
              if (t != null) changeType(t + (array ? '[]' : ''));
            },
          ),
          SwitchListTile(
            contentPadding: EdgeInsets.zero,
            title: const Text('一维数组'),
            value: array,
            onChanged: (v) => changeType(base + (v ? '[]' : '')),
          ),
        ] else
          Padding(
            padding: const EdgeInsets.only(bottom: 12),
            child: Text(
              '类型：$type',
              key: ValueKey('${widget.fieldKey}-fixed-type'),
            ),
          ),
        if (array)
          _array(base)
        else if (base == 'Boolean')
          SwitchListTile(
            key: ValueKey(widget.fieldKey),
            contentPadding: EdgeInsets.zero,
            title: const Text('值：true / false'),
            value: raw == true || raw == 'true',
            onChanged: change,
          )
        else if (base == 'LocalizedText') ...[
          field(
            '文本',
            '${uaMap(raw)['text'] ?? uaMap(raw)['Text'] ?? ''}',
            (v) => change({...uaMap(raw), 'text': v}),
            suffix: '-text',
            multi: true,
          ),
          field(
            '语言区域（例如 zh-CN）',
            '${uaMap(raw)['locale'] ?? uaMap(raw)['Locale'] ?? ''}',
            (v) => change({...uaMap(raw), 'locale': v}),
            suffix: '-locale',
          ),
        ] else if (base == 'QualifiedName') ...[
          field(
            '名称',
            '${uaMap(raw)['name'] ?? uaMap(raw)['Name'] ?? ''}',
            (v) => change({...uaMap(raw), 'name': v}),
            suffix: '-name',
          ),
          field(
            '命名空间（0–65535）',
            '${uaMap(raw)['namespace'] ?? uaMap(raw)['NamespaceIndex'] ?? 0}',
            (v) => change({...uaMap(raw), 'namespace': v}),
            suffix: '-namespace',
          ),
        ] else
          field(
            base == 'ByteString'
                ? 'Base64 字节串'
                : base == 'String'
                ? '文本值'
                : base == 'NodeId'
                ? 'NodeId'
                : base == 'Guid'
                ? 'GUID'
                : base == 'DateTime'
                ? 'RFC3339 时间（含时区）'
                : '精确数值 · 十进制',
            uaText(raw),
            change,
            multi: base == 'String' || base == 'ByteString',
          ),
        if (base == 'ByteString' || base == 'Byte')
          const Text('ByteString 是单个 Base64 字节串；Byte[] 是独立字节元素数组'),
      ],
    );
  }

  Widget _array(String base) {
    final values = raw is List ? List<dynamic>.from(raw) : <dynamic>[];
    final pages = (values.length / 25).ceil().clamp(1, 400);
    page = page.clamp(0, pages - 1);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('${values.length} 项 · 顺序就是发送顺序 · 最多 10000 项'),
        if (values.length > 10000)
          TextButton(
            onPressed: () => change(<dynamic>[]),
            child: const Text('缓存数组超限，明确新建空数组'),
          ),
        for (var i = page * 25; i < values.length && i < (page + 1) * 25; i++)
          Card(
            child: ListTile(
              title: Text(
                '${i + 1}. ${uaText(values[i])}',
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
              ),
              onTap: () => _element(base, values, i),
              trailing: IconButton(
                tooltip: '删除元素 ${i + 1}',
                icon: const Icon(Icons.remove_circle_outline),
                onPressed: () {
                  values.removeAt(i);
                  change(values);
                },
              ),
            ),
          ),
        Row(
          children: [
            IconButton(
              tooltip: '上一页元素',
              onPressed: page > 0 ? () => setState(() => page--) : null,
              icon: const Icon(Icons.chevron_left),
            ),
            Text('${page + 1} / $pages'),
            IconButton(
              tooltip: '下一页元素',
              onPressed: page + 1 < pages ? () => setState(() => page++) : null,
              icon: const Icon(Icons.chevron_right),
            ),
            const Spacer(),
            TextButton.icon(
              onPressed: values.length < 10000
                  ? () => _element(base, values, null)
                  : null,
              icon: const Icon(Icons.add),
              label: const Text('添加元素'),
            ),
          ],
        ),
      ],
    );
  }

  Future<void> _element(String base, List<dynamic> values, int? index) async {
    dynamic value = index == null ? uaDefault(base) : values[index];
    String? error;
    final result = await showDialog<dynamic>(
      context: context,
      builder: (context) {
        _elementRoute = ModalRoute.of(context);
        return StatefulBuilder(
          builder: (context, setDialog) => AlertDialog(
            title: Text('编辑 $base 元素'),
            content: SingleChildScrollView(
              child: SizedBox(
                width: 460,
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    UaTypedEditor(
                      initialType: base,
                      initialValue: value,
                      fieldKey: 'ua-array-element',
                      onChanged: (d) => value = d['value'],
                    ),
                    if (error != null)
                      Text(
                        error!,
                        style: TextStyle(
                          color: Theme.of(context).colorScheme.error,
                        ),
                      ),
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
                onPressed: () {
                  try {
                    Navigator.pop(context, uaValidate(base, value));
                  } catch (e) {
                    setDialog(() => error = e.toString());
                  }
                },
                child: const Text('应用元素'),
              ),
            ],
          ),
        );
      },
    );
    _elementRoute = null;
    if (!mounted || result == null) return;
    if (index == null) {
      values.add(result);
      page = (values.length - 1) ~/ 25;
    } else {
      values[index] = result;
    }
    change(values);
  }
}
