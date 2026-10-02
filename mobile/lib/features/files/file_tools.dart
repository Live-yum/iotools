import 'dart:async';
import 'package:flutter/material.dart';
import '../../core/engine.dart';
import '../../core/json.dart';
import '../../core/session.dart';
import '../../shared/widgets.dart';

const gib = 1024 * 1024 * 1024, multipartLimit = 4 * 1024 * 1024;
Future<int?> chooseTransferLimit(
  BuildContext context, {
  int initial = gib,
}) async {
  int selected = initial;
  return memoryDialog<int>(
    context: context,
    builder: (c) => AlertDialog(
      title: const Text('文件传输上限'),
      content: ChoiceField(
        label: '最大文件大小',
        value: '${selected ~/ gib} GiB',
        options: List.generate(8, (i) => '${i + 1} GiB'),
        onChanged: (v) => selected = int.parse(v.split(' ').first) * gib,
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(c), child: const Text('取消')),
        FilledButton(
          onPressed: () => Navigator.pop(c, selected),
          child: const Text('选择文件'),
        ),
      ],
    ),
  );
}

Future<Object?> transferFile(
  BuildContext context,
  PlatformServices platform,
  String method,
  JsonMap arguments,
) => memoryDialog<Object?>(
  context: context,
  barrierDismissible: false,
  builder: (c) =>
      _TransferDialog(platform: platform, method: method, arguments: arguments),
);

class _TransferDialog extends StatefulWidget {
  const _TransferDialog({
    required this.platform,
    required this.method,
    required this.arguments,
  });
  final PlatformServices platform;
  final String method;
  final JsonMap arguments;
  @override
  State<_TransferDialog> createState() => _TransferDialogState();
}

class _TransferDialogState extends State<_TransferDialog> {
  bool cancelled = false;
  String? error;
  @override
  void initState() {
    super.initState();
    run();
  }

  Future<void> run() async {
    try {
      final value = await widget.platform.invoke(
        widget.method,
        widget.arguments,
      );
      if (mounted) Navigator.pop(context, cancelled ? null : value);
    } catch (e) {
      if (mounted) setState(() => error = '$e');
    }
  }

  @override
  Widget build(BuildContext context) => PopScope(
    canPop: error != null,
    child: AlertDialog(
      title: Text(error == null ? '文件选择 / 流式传输' : '文件操作失败'),
      content: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (error == null) const LinearProgressIndicator(),
          const SizedBox(height: 16),
          Text(
            error ??
                (cancelled ? '正在停止，临时文件将清理' : '请在系统选择器中选择文件。大文件按流处理，不载入编辑器。'),
          ),
        ],
      ),
      actions: [
        TextButton(
          onPressed: () async {
            if (error != null) {
              Navigator.pop(context);
              return;
            }
            await widget.platform.invoke('files.cancel');
            if (mounted) setState(() => cancelled = true);
          },
          child: Text(error == null ? '停止传输' : '关闭'),
        ),
      ],
    ),
  );
}

Future<JsonMap?> pickAttachment(
  BuildContext context,
  AppSession session, {
  int? fixedLimit,
}) async {
  final limit = fixedLimit ?? await chooseTransferLimit(context);
  if (limit == null || !context.mounted) return null;
  final result = await transferFile(context, session.platform, 'files.pick', {
    'limit': limit,
  });
  final map = mapOf(result);
  return map.isEmpty ? null : map;
}

String fileExpression(String path) => '{{ file(${exactEncode(path)}) }}';
Future<JsonMap?> editMultipart(
  BuildContext context,
  AppSession session,
  JsonMap initial,
) async {
  final entries = initial.entries.toList();
  return memoryDialog<JsonMap>(
    context: context,
    builder: (c) => StatefulBuilder(
      builder: (c, set) => AlertDialog(
        title: const Text('Multipart 文本与二进制字段'),
        content: SizedBox(
          width: 600,
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                const Text(
                  '二进制字段保留 file() 引用。Multipart 总量由内核限制为 4 MiB；更大文件请使用正文文件流。',
                ),
                for (final row in entries.asMap().entries)
                  ListTile(
                    title: Text(row.value.key),
                    subtitle: Text(
                      row.value.value.toString(),
                      maxLines: 2,
                      overflow: TextOverflow.ellipsis,
                    ),
                    onTap: () async {
                      final value = await inputDialog(
                        c,
                        '文本值',
                        initial: row.value.value.toString(),
                        multiline: true,
                      );
                      if (value != null)
                        set(
                          () =>
                              entries[row.key] = MapEntry(row.value.key, value),
                        );
                    },
                    trailing: IconButton(
                      tooltip: '移除字段',
                      onPressed: () => set(() => entries.removeAt(row.key)),
                      icon: const Icon(Icons.close),
                    ),
                  ),
                OutlinedButton.icon(
                  onPressed: () async {
                    final name = await inputDialog(c, '字段名称');
                    if (name == null || name.isEmpty || !c.mounted) return;
                    if (entries.any((e) => e.key == name)) {
                      reportError(c, '字段名称重复');
                      return;
                    }
                    final value = await inputDialog(c, '文本值', multiline: true);
                    if (value != null)
                      set(() => entries.add(MapEntry(name, value)));
                  },
                  icon: const Icon(Icons.text_fields),
                  label: const Text('添加文本字段'),
                ),
                OutlinedButton.icon(
                  onPressed: () async {
                    final name = await inputDialog(c, '二进制字段名称');
                    if (name == null || name.isEmpty || !c.mounted) return;
                    if (entries.any((e) => e.key == name)) {
                      reportError(c, '字段名称重复');
                      return;
                    }
                    final file = await guarded(
                      c,
                      () => pickAttachment(
                        c,
                        session,
                        fixedLimit: multipartLimit,
                      ),
                    );
                    if (file != null)
                      set(
                        () => entries.add(
                          MapEntry(
                            name,
                            fileExpression(file['path'].toString()),
                          ),
                        ),
                      );
                  },
                  icon: const Icon(Icons.attach_file),
                  label: const Text('选择二进制文件字段'),
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
          FilledButton(
            onPressed: () =>
                Navigator.pop(c, Map<String, dynamic>.fromEntries(entries)),
            child: const Text('应用到表单'),
          ),
        ],
      ),
    ),
  );
}
