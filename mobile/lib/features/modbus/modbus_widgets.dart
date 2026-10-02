import 'dart:convert';
import 'package:flutter/material.dart';
import '../../shared/widgets.dart' show spacedChildren;

/// Wait for reverse transition and overlay disposal before input owners clean up.
Future<T?> mbDialog<T>({
  required BuildContext context,
  required WidgetBuilder builder,
}) async {
  final route = DialogRoute<T>(context: context, builder: builder);
  final result = await Navigator.of(
    context,
    rootNavigator: true,
  ).push<T>(route);
  await route.completed;
  return result;
}

Widget mbField(
  String label,
  TextEditingController controller, {
  bool multiline = false,
  bool numeric = false,
  bool enabled = true,
  String? hint,
}) => Padding(
  padding: const EdgeInsets.symmetric(vertical: 7),
  child: TextField(
    key: ValueKey(label),
    controller: controller,
    enabled: enabled,
    minLines: multiline ? 3 : 1,
    maxLines: multiline ? 8 : 1,
    keyboardType: multiline
        ? TextInputType.multiline
        : numeric
        ? TextInputType.number
        : TextInputType.text,
    decoration: InputDecoration(
      labelText: label,
      hintText: hint,
      border: const OutlineInputBorder(),
      contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 16),
    ),
  ),
);
Widget mbSelect(
  String label,
  String value,
  List<String> choices,
  ValueChanged<String> changed,
) => Padding(
  padding: const EdgeInsets.symmetric(vertical: 7),
  child: DropdownButtonFormField<String>(
    key: ValueKey(label),
    initialValue: choices.contains(value) ? value : choices.first,
    decoration: InputDecoration(
      labelText: label,
      border: const OutlineInputBorder(),
    ),
    items: choices
        .map((choice) => DropdownMenuItem(value: choice, child: Text(choice)))
        .toList(),
    onChanged: (value) {
      if (value != null) changed(value);
    },
  ),
);
Widget mbPair(String label, Object? value) => Padding(
  padding: const EdgeInsets.symmetric(vertical: 6),
  child: Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      Text(
        label,
        style: const TextStyle(color: Color(0xff91a5bb), fontSize: 12),
      ),
      const SizedBox(height: 3),
      SelectableText(value?.toString() ?? '—'),
    ],
  ),
);
Widget mbCard(String title, List<Widget> children) => Card(
  child: Padding(
    padding: const EdgeInsets.all(16),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          title,
          style: const TextStyle(fontSize: 18, fontWeight: FontWeight.w600),
        ),
        const SizedBox(height: 10),
        ...spacedChildren(children),
      ],
    ),
  ),
);

Future<bool> mbConfirm(
  BuildContext context,
  String title,
  List<Widget> children, {
  String confirm = '确认',
  bool enabled = true,
}) async =>
    await mbDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(title),
        content: SizedBox(
          width: 560,
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: children,
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(context, false),
            child: const Text('取消'),
          ),
          FilledButton(
            onPressed: enabled ? () => Navigator.pop(context, true) : null,
            child: Text(confirm),
          ),
        ],
      ),
    ) ??
    false;

Future<void> mbShow(
  BuildContext context,
  String title,
  List<Widget> children,
) => mbDialog<void>(
  context: context,
  builder: (context) => AlertDialog(
    title: Text(title),
    content: SizedBox(
      width: 620,
      child: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: children,
        ),
      ),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('关闭'),
      ),
    ],
  ),
);

List<Widget> mbStructured(Object? data) {
  if (data is Map)
    return data.entries
        .map(
          (entry) => mbPair(
            entry.key.toString(),
            entry.value is Map || entry.value is List
                ? const JsonEncoder.withIndent('  ').convert(entry.value)
                : entry.value,
          ),
        )
        .toList();
  if (data is List)
    return data
        .take(2000)
        .map(
          (row) =>
              row is Map ? mbCard('条目', mbStructured(row)) : mbPair('值', row),
        )
        .toList();
  return [SelectableText(data?.toString() ?? '—')];
}

/// The submit stays disabled for its entire asynchronous operation. Errors keep
/// inputs intact, cancellation never calls the supplied action.
class ModbusFormDialog extends StatefulWidget {
  const ModbusFormDialog({
    super.key,
    required this.title,
    required this.children,
    required this.confirm,
    required this.onSubmit,
  });
  final String title, confirm;
  final List<Widget> children;
  final Future<bool> Function() onSubmit;
  @override
  State<ModbusFormDialog> createState() => _ModbusFormDialogState();
}

class _ModbusFormDialogState extends State<ModbusFormDialog> {
  bool busy = false;
  String? error;
  @override
  Widget build(BuildContext context) => PopScope(
    canPop: !busy,
    child: AlertDialog(
      title: Text(widget.title),
      content: SizedBox(
        width: 620,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              ...widget.children,
              if (error != null)
                Padding(
                  padding: const EdgeInsets.only(top: 12),
                  child: Text(
                    error!,
                    style: TextStyle(
                      color: Theme.of(context).colorScheme.error,
                    ),
                  ),
                ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: busy ? null : () => Navigator.pop(context),
          child: const Text('取消'),
        ),
        FilledButton(
          onPressed: busy
              ? null
              : () async {
                  setState(() {
                    busy = true;
                    error = null;
                  });
                  try {
                    final close = await widget.onSubmit();
                    if (!mounted) return;
                    if (close) {
                      Navigator.pop(context);
                    } else {
                      setState(() => busy = false);
                    }
                  } catch (e) {
                    if (mounted)
                      setState(() {
                        busy = false;
                        error = e.toString();
                      });
                  }
                },
          child: busy
              ? const SizedBox(
                  width: 20,
                  height: 20,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : Text(widget.confirm),
        ),
      ],
    ),
  );
}
