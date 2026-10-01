import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import '../core/json.dart';

const cyan = Color(0xff24d5ed),
    navy = Color(0xff08141f),
    surface = Color(0xff102333);
ThemeData appTheme(Brightness brightness) {
  final dark = brightness == Brightness.dark;
  return ThemeData(
    useMaterial3: true,
    brightness: brightness,
    colorScheme:
        ColorScheme.fromSeed(
          seedColor: cyan,
          brightness: brightness,
          surface: dark ? surface : const Color(0xfff5f8fc),
        ).copyWith(
          primary: dark ? cyan : const Color(0xff006f85),
          onPrimary: dark ? navy : Colors.white,
        ),
    scaffoldBackgroundColor: dark ? navy : const Color(0xffedf3f8),
    appBarTheme: AppBarTheme(
      backgroundColor: dark ? navy : Colors.white,
      centerTitle: false,
    ),
    cardTheme: CardThemeData(
      color: dark ? surface : Colors.white,
      surfaceTintColor: Colors.transparent,
      elevation: 0,
      margin: const EdgeInsets.symmetric(vertical: 6),
      shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(18)),
    ),
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      fillColor: dark ? const Color(0xff0b1b29) : Colors.white,
      border: OutlineInputBorder(borderRadius: BorderRadius.circular(12)),
      contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 16),
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(minimumSize: const Size(48, 48)),
    ),
    outlinedButtonTheme: OutlinedButtonThemeData(
      style: OutlinedButton.styleFrom(minimumSize: const Size(48, 48)),
    ),
    textButtonTheme: TextButtonThemeData(
      style: TextButton.styleFrom(minimumSize: const Size(48, 48)),
    ),
    navigationBarTheme: NavigationBarThemeData(
      backgroundColor: dark ? surface : Colors.white,
      indicatorColor: cyan.withValues(alpha: .2),
    ),
  );
}

class Section extends StatelessWidget {
  const Section(this.title, {required this.children, this.subtitle, super.key});
  final String title;
  final String? subtitle;
  final List<Widget> children;
  @override
  Widget build(BuildContext context) => Card(
    child: Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(title, style: Theme.of(context).textTheme.titleMedium),
          if (subtitle != null)
            Padding(
              padding: const EdgeInsets.only(top: 6),
              child: Text(
                subtitle!,
                style: Theme.of(context).textTheme.bodySmall,
              ),
            ),
          const SizedBox(height: 12),
          ...children,
        ],
      ),
    ),
  );
}

class EmptyState extends StatelessWidget {
  const EmptyState(this.title, this.detail, {super.key});
  final String title, detail;
  @override
  Widget build(BuildContext context) => Center(
    child: Padding(
      padding: const EdgeInsets.all(32),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          const Icon(Icons.space_dashboard_outlined, size: 44, color: cyan),
          const SizedBox(height: 16),
          Text(title, style: Theme.of(context).textTheme.titleLarge),
          const SizedBox(height: 8),
          Text(detail, textAlign: TextAlign.center),
        ],
      ),
    ),
  );
}

Future<void> reportError(BuildContext context, Object e) async {
  if (!context.mounted) return;
  ScaffoldMessenger.of(context).showSnackBar(
    SnackBar(content: Text('$e'), duration: const Duration(seconds: 7)),
  );
}

Future<T?> guarded<T>(BuildContext context, Future<T> Function() action) async {
  try {
    return await action();
  } catch (e) {
    await reportError(context, e);
    return null;
  }
}

Future<bool> confirm(
  BuildContext context,
  String title,
  String detail, {
  String action = '确认',
}) async =>
    await memoryDialog<bool>(
      context: context,
      builder: (c) => AlertDialog(
        title: Text(title),
        content: SingleChildScrollView(child: SelectableText(detail)),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c, false),
            child: const Text('取消'),
          ),
          FilledButton(
            key: const ValueKey('confirm_action'),
            onPressed: () => Navigator.pop(c, true),
            child: Text(action),
          ),
        ],
      ),
    ) ??
    false;
Future<String?> inputDialog(
  BuildContext context,
  String title, {
  String initial = '',
  String hint = '',
  bool multiline = false,
  bool secret = false,
}) async {
  final controller = TextEditingController(text: initial);
  final result = await memoryDialog<String>(
    context: context,
    builder: (c) => AlertDialog(
      title: Text(title),
      content: SizedBox(
        width: 560,
        child: TextField(
          key: const ValueKey('input_dialog'),
          controller: controller,
          autofocus: true,
          obscureText: secret,
          minLines: multiline ? 4 : 1,
          maxLines: multiline ? 12 : 1,
          decoration: InputDecoration(hintText: hint),
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(c), child: const Text('取消')),
        FilledButton(
          onPressed: () => Navigator.pop(c, controller.text),
          child: const Text('应用'),
        ),
      ],
    ),
  );
  controller.dispose();
  return result;
}

Future<void> showData(
  BuildContext context,
  String title,
  Object? data, {
  Future<void> Function(String, String)? export,
}) => memoryDialog(
  context: context,
  builder: (c) => AlertDialog(
    title: Text(title),
    content: SizedBox(
      width: 760,
      child: SingleChildScrollView(child: DataView(data)),
    ),
    actions: [
      TextButton(
        onPressed: () => Clipboard.setData(
          ClipboardData(text: data is String ? data : pretty(data)),
        ),
        child: const Text('复制'),
      ),
      if (export != null)
        TextButton(
          onPressed: () => guarded(
            c,
            () => export(
              '$title.json',
              data is String ? data : exactEncode(data),
            ),
          ),
          child: const Text('导出'),
        ),
      TextButton(onPressed: () => Navigator.pop(c), child: const Text('关闭')),
    ],
  ),
);

class DataView extends StatelessWidget {
  const DataView(this.data, {super.key});
  final Object? data;
  @override
  Widget build(BuildContext context) {
    final m = mapOf(data);
    if (m['columns'] is List && m['rows'] is List) {
      return SqlTable(m);
    }
    if (data is List &&
        (data as List).isNotEmpty &&
        mapOf((data as List).first).containsKey('columns')) {
      return Column(
        children: (data as List).map((e) => SqlTable(mapOf(e))).toList(),
      );
    }
    final text = data is String ? data as String : pretty(data);
    return SelectableText(
      text.length > 100000
          ? '${text.substring(0, 100000)}\n显示前 100000 字符；复制/导出保留完整内容'
          : text,
      style: const TextStyle(fontFamily: 'monospace', fontSize: 13),
    );
  }
}

class SqlTable extends StatefulWidget {
  const SqlTable(this.data, {super.key});
  final JsonMap data;
  @override
  State<SqlTable> createState() => _SqlTableState();
}

class _SqlTableState extends State<SqlTable> {
  String query = '';
  int page = 0;
  @override
  Widget build(BuildContext context) {
    final cols = (widget.data['columns'] as List? ?? [])
        .map((e) => e.toString())
        .toList();
    final all = (widget.data['rows'] as List? ?? [])
        .where((r) => r.toString().toLowerCase().contains(query.toLowerCase()))
        .toList();
    final start = (page * 50).clamp(0, all.length);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      mainAxisSize: MainAxisSize.min,
      children: [
        TextField(
          decoration: const InputDecoration(labelText: '筛选当前 SQL 结果'),
          onChanged: (v) => setState(() {
            query = v;
            page = 0;
          }),
        ),
        Text('${all.length} 行 · ${cols.length} 列'),
        SingleChildScrollView(
          scrollDirection: Axis.horizontal,
          child: DataTable(
            columns: cols.map((c) => DataColumn(label: Text(c))).toList(),
            rows: all.skip(start).take(50).map((r) {
              final values = r is List
                  ? r
                  : cols.map((c) => mapOf(r)[c]).toList();
              return DataRow(
                cells: List.generate(
                  cols.length,
                  (i) => DataCell(
                    SelectableText(
                      i < values.length ? '${values[i] ?? 'NULL'}' : '',
                    ),
                  ),
                ),
              );
            }).toList(),
          ),
        ),
        Row(
          children: [
            IconButton(
              onPressed: page > 0 ? () => setState(() => page--) : null,
              icon: const Icon(Icons.chevron_left),
            ),
            Text('第 ${page + 1} 页'),
            IconButton(
              onPressed: start + 50 < all.length
                  ? () => setState(() => page++)
                  : null,
              icon: const Icon(Icons.chevron_right),
            ),
          ],
        ),
      ],
    );
  }
}

class ChoiceField extends StatelessWidget {
  const ChoiceField({
    required this.label,
    required this.value,
    required this.options,
    required this.onChanged,
    super.key,
  });
  final String label, value;
  final List<String> options;
  final ValueChanged<String> onChanged;
  @override
  Widget build(BuildContext context) => DropdownButtonFormField<String>(
    initialValue: options.contains(value) ? value : null,
    isExpanded: true,
    decoration: InputDecoration(labelText: label),
    items: options
        .map(
          (s) => DropdownMenuItem(
            value: s,
            child: Text(s, overflow: TextOverflow.ellipsis),
          ),
        )
        .toList(),
    onChanged: (v) {
      if (v != null) onChanged(v);
    },
  );
}

/// Wait until reverse animation completes before disposing memory-only editors.
Future<T?> memoryDialog<T>({
  required BuildContext context,
  required WidgetBuilder builder,
  bool barrierDismissible = true,
}) async {
  final route = DialogRoute<T>(
    context: context,
    builder: builder,
    barrierDismissible: barrierDismissible,
  );
  final result = await Navigator.of(context, rootNavigator: true).push(route);
  await route.completed;
  return result;
}
