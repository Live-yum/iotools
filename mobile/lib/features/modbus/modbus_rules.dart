import 'dart:convert';
import 'package:flutter/material.dart';
import 'modbus_controller.dart';
import 'modbus_host.dart';
import 'modbus_models.dart';
import 'modbus_widgets.dart';

class ModbusRuleEditor extends StatefulWidget {
  const ModbusRuleEditor({
    super.key,
    required this.host,
    required this.model,
    required this.address,
  });
  final ModbusHost host;
  final ModbusController model;
  final int address;
  @override
  State<ModbusRuleEditor> createState() => _ModbusRuleEditorState();
}

class _ModbusRuleEditorState extends State<ModbusRuleEditor> {
  late final TextEditingController address, operands, decimals, prefix, suffix;
  late final Map<String, dynamic> original, enums, bits;
  final List<TextEditingController> operations = [], retired = [];
  String repr = 'u16', order = '继承请求';
  @override
  void initState() {
    super.initState();
    original = ruleMap(widget.model.params['rules'])[widget.address] ?? {};
    address = TextEditingController(text: '${widget.address}');
    operands = TextEditingController(
      text: (original['next'] as List? ?? []).join(','),
    );
    decimals = TextEditingController(
      text: original['decimals']?.toString() ?? '',
    );
    prefix = TextEditingController(text: original['prefix']?.toString() ?? '');
    suffix = TextEditingController(text: original['suffix']?.toString() ?? '');
    repr = original['repr']?.toString() ?? 'u16';
    order = original['word_order']?.toString() ?? '继承请求';
    enums = mbClone(mbMap(original['enum']));
    bits = mbClone(mbMap(original['bits']));
    operations.addAll(
      (original['ops'] as List? ?? []).map(
        (v) => TextEditingController(text: v.toString()),
      ),
    );
  }

  @override
  void dispose() {
    for (final c in [
      address,
      operands,
      decimals,
      prefix,
      suffix,
      ...operations,
      ...retired,
    ]) {
      c.dispose();
    }
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => ModbusFormDialog(
    title: '寄存器规则编辑',
    confirm: '校验并预览',
    onSubmit: preview,
    children: [
      mbField('规则地址', address, numeric: true),
      mbSelect(
        '原始数值类型',
        repr,
        modbusValueTypes,
        (v) => setState(() => repr = v),
      ),
      mbField('后续操作数地址（逗号分隔，空白为连续）', operands),
      mbSelect('规则字节顺序', order, [
        '继承请求',
        ...modbusWordOrders,
      ], (v) => setState(() => order = v)),
      mbField('小数位（空白自动，0–15）', decimals, numeric: true),
      mbField('前缀', prefix),
      mbField('后缀', suffix),
      const Text('依次执行的算术操作（+ - * / ^ 与有限操作数）'),
      for (var i = 0; i < operations.length; i++)
        Row(
          children: [
            Expanded(child: mbField('步骤 ${i + 1}', operations[i])),
            IconButton(
              tooltip: '上移步骤 ${i + 1}',
              onPressed: i == 0
                  ? null
                  : () => setState(() {
                      final item = operations.removeAt(i);
                      operations.insert(i - 1, item);
                    }),
              icon: const Icon(Icons.arrow_upward),
            ),
            IconButton(
              tooltip: '删除步骤 ${i + 1}',
              onPressed: () => setState(() {
                retired.add(operations.removeAt(i));
              }),
              icon: const Icon(Icons.delete_outline),
            ),
          ],
        ),
      OutlinedButton(
        onPressed: operations.length >= 64
            ? null
            : () => setState(
                () => operations.add(TextEditingController(text: '+0')),
              ),
        child: const Text('添加算术步骤'),
      ),
      _mapping('枚举', enums, false),
      _mapping('位标签', bits, true),
      const Text('预览只使用同一次缓存响应。缺失操作数保持空缺，校验与预览不会连接设备。'),
      if (original.isNotEmpty)
        OutlinedButton(
          onPressed: () async {
            if (!await mbConfirm(context, '删除此临时规则', [
              Text('仅删除地址 ${widget.address} 的工作区规则。明确保存后才修改配置。'),
            ], confirm: '删除临时规则'))
              return;
            final p = mbClone(widget.model.params)
              ..['rules'] = replaceRule(
                mbRows(widget.model.params['rules']),
                widget.address,
                null,
              );
            widget.model.editParams(p);
            await widget.model.reinterpret();
            if (context.mounted) Navigator.pop(context);
          },
          child: const Text('删除此规则'),
        ),
    ],
  );
  Widget _mapping(String title, Map<String, dynamic> values, bool bit) =>
      mbCard('$title映射', [
        SizedBox(
          height: values.isEmpty ? 48 : 160,
          child: values.isEmpty
              ? const Text('尚无映射')
              : ListView.builder(
                  itemCount: values.length,
                  itemBuilder: (_, index) {
                    final keys = values.keys.toList()
                      ..sort(
                        (a, b) => BigInt.parse(a).compareTo(BigInt.parse(b)),
                      );
                    final key = keys[index];
                    return ListTile(
                      title: Text('$key → ${values[key]}'),
                      trailing: const Icon(Icons.edit),
                      onTap: () => _editMapping(title, values, bit, key),
                    );
                  },
                ),
        ),
        OutlinedButton(
          onPressed: () => _editMapping(title, values, bit, null),
          child: Text('添加$title'),
        ),
      ]);
  Future<void> _editMapping(
    String title,
    Map<String, dynamic> values,
    bool bit,
    String? old,
  ) async {
    final key = TextEditingController(text: old ?? ''),
        label = TextEditingController(
          text: old == null ? '' : values[old].toString(),
        );
    await mbDialog<void>(
      context: context,
      builder: (dialogContext) => ModbusFormDialog(
        title: '$title映射',
        confirm: '应用映射',
        children: [
          mbField(bit ? '位索引（0–63）' : '精确整数键', key),
          mbField('显示名称', label),
          if (old != null)
            OutlinedButton(
              onPressed: () {
                setState(() => values.remove(old));
                Navigator.pop(dialogContext);
              },
              child: const Text('删除此映射'),
            ),
        ],
        onSubmit: () async {
          final integer = BigInt.tryParse(key.text.trim());
          if (integer == null ||
              (bit && (integer < BigInt.zero || integer > BigInt.from(63))))
            throw const FormatException('请输入合法整数键');
          final normalized = integer.toString();
          if (values.containsKey(normalized) && normalized != old)
            throw const FormatException('此整数键已有映射');
          if (label.text.length > 1024)
            throw const FormatException('名称最多 1024 字符');
          setState(() {
            if (old != null) values.remove(old);
            values[normalized] = label.text;
          });
          return true;
        },
      ),
    );
    key.dispose();
    label.dispose();
  }

  Future<bool> preview() async {
    final a = boundedInt(address.text, 0, 65535, '规则地址');
    final rule = <String, dynamic>{'address': a, 'repr': repr};
    if (operands.text.trim().isNotEmpty)
      rule['next'] = operands.text
          .split(',')
          .map((v) => boundedInt(v.trim(), 0, 65535, '操作数地址'))
          .toList();
    if (order != '继承请求') rule['word_order'] = order;
    if (decimals.text.trim().isNotEmpty)
      rule['decimals'] = boundedInt(decimals.text, 0, 15, '小数位');
    rule['prefix'] = prefix.text;
    rule['suffix'] = suffix.text;
    final ops = <String>[];
    for (final controller in operations) {
      final text = controller.text.trim();
      if (text.length < 2 || !'+-*/^'.contains(text[0]))
        throw const FormatException('算术步骤需要运算符与操作数');
      final number = double.tryParse(text.substring(1).trim());
      if (number == null || !number.isFinite || (text[0] == '/' && number == 0))
        throw const FormatException('操作数必须有限，除数不能为零');
      ops.add(text);
    }
    rule['ops'] = ops;
    rule['enum'] = mbClone(enums);
    rule['bits'] = mbClone(bits);
    final all = mbRows(widget.model.params['rules']);
    if (a != widget.address && ruleMap(all).containsKey(a))
      throw const FormatException('新地址已有规则，不能静默覆盖');
    final request = mbClone(widget.model.draft),
        p = mbClone(widget.model.params);
    p['rules'] = replaceRule(replaceRule(all, widget.address, null), a, rule);
    request['params'] = p;
    final revision = jsonEncode(widget.model.draft),
        frame = widget.model.current;
    await widget.host.command({'op': 'modbus.rules', 'request': request});
    List<Map<String, dynamic>> rows = [];
    if (frame != null && !frame.gap && frame.words.isNotEmpty)
      rows = mbRows(
        await widget.host.command({
          'op': 'modbus.interpret',
          'request': request,
          'words': frame.words,
        }),
      );
    if (!mounted) return false;
    final matching = rows
        .where((row) => row['address'].toString() == '$a')
        .toList();
    final accepted = await mbConfirm(context, '规则预览 · 尚未保存', [
      mbPair('地址 / 类型', '$a / $repr'),
      if (frame != null)
        mbPair(
          '同次采样',
          '${frame.time}\n${frame.source.endpoint} · ${frame.source.unit}',
        ),
      if (matching.isEmpty)
        const Text('没有匹配缓存或操作数缺失。规则格式已通过共享引擎校验。')
      else
        for (final row in matching) ...[
          mbPair('自定义结果', row['custom'] ?? '操作数缺失'),
          mbPair('数值结果', row['custom_numeric'] ?? '操作数缺失'),
        ],
      mbPair('算术步骤', ops.join(' → ')),
      mbPair('枚举 / 位标签数量', '${enums.length} / ${bits.length}'),
    ], confirm: '应用临时规则');
    if (!accepted) return false;
    if (revision != jsonEncode(widget.model.draft) ||
        (frame != null && widget.model.current != frame))
      throw const FormatException('工作区或缓存已变化，请重新预览');
    widget.model.edit(request);
    await widget.model.reinterpret();
    return true;
  }
}
