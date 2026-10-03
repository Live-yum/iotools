import 'package:flutter/material.dart';
import '../../core/json.dart';
import '../../core/session.dart';
import '../../shared/widgets.dart';

String historyBytes(dynamic value) {
  final bytes = value is num ? value.toInt() : 0;
  if (bytes < 1024) return '$bytes B';
  if (bytes < 1024 * 1024) return '${(bytes / 1024).toStringAsFixed(1)} KiB';
  return '${(bytes / (1024 * 1024)).toStringAsFixed(1)} MiB';
}

class HistoryStorageCard extends StatelessWidget {
  const HistoryStorageCard({required this.storage, super.key});
  final JsonMap storage;
  @override
  Widget build(BuildContext context) => Card(
    child: Padding(
      padding: const EdgeInsets.all(16),
      child: Text(
        '整个历史库：${storage['entries'] ?? 0} 条\n'
        '实际磁盘 ${historyBytes(storage['total_bytes'])} '
        '（数据库 ${historyBytes(storage['database_bytes'])}，'
        '辅助文件 ${historyBytes(storage['sidecar_bytes'])}）\n'
        '响应内容 ${historyBytes(storage['payload_bytes'])} · '
        '可复用空闲页 ${historyBytes(storage['reusable_bytes'])}\n'
        '${mapOf(storage['policy'])['mode'] == 'prune' ? '已启用自动清理：每次记录后执行已确认策略' : '默认保留已有记录：达到容量后暂停记录，不自动删除'}',
      ),
    ),
  );
}

Future<void> configureHistoryStorage(
  BuildContext context,
  AppSession session,
  JsonMap storage,
) async {
  final current = mapOf(storage['policy']);
  final age = TextEditingController(text: '${current['max_age_days'] ?? 0}');
  final count = TextEditingController(text: '${current['max_entries'] ?? 10000}');
  final bytes = TextEditingController(text: '${current['max_bytes'] ?? 134217728}');
  var prune = current['mode'] == 'prune';
  String? error;
  JsonMap? policy;
  try {
    policy = await memoryDialog<JsonMap>(
      context: context,
      builder: (c) => StatefulBuilder(builder: (c, update) => AlertDialog(
        title: const Text('历史容量与保留策略'),
        content: SizedBox(width: 500, child: SingleChildScrollView(child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Text('作用于整个历史库的所有集合。0 表示不限。内容字节包含响应头、正文和转换结果，实际数据库可能更大。'),
            TextField(controller: age, keyboardType: TextInputType.number,
              decoration: const InputDecoration(labelText: '最长保留天数（0–36500）')),
            TextField(controller: count, keyboardType: TextInputType.number,
              decoration: const InputDecoration(labelText: '最多条数（0–10000000）')),
            TextField(controller: bytes, keyboardType: TextInputType.number,
              decoration: const InputDecoration(labelText: '最多响应内容字节（0–1099511627776）')),
            CheckboxListTile(value: prune,
              title: const Text('明确启用自动永久清理'),
              subtitle: const Text('开启后立即清理预览中的记录，并在以后记录时删除超龄/超限记录。关闭则保留已有记录，达到条数或容量后暂停记录；超龄旧记录保留到手动清理。'),
              onChanged: (v) => update(() => prune = v == true)),
            if (error != null) Text(error!, style: const TextStyle(color: Colors.redAccent)),
          ],
        ))),
        actions: [
          TextButton(onPressed: () => Navigator.pop(c), child: const Text('取消')),
          FilledButton(onPressed: () {
            final a = int.tryParse(age.text);
            final n = int.tryParse(count.text);
            final b = int.tryParse(bytes.text);
            if (a == null || a < 0 || a > 36500 || n == null || n < 0 || n > 10000000 || b == null || b < 0 || b > 1099511627776) {
              update(() => error = '请输入范围内的非负整数');
              return;
            }
            Navigator.pop(c, <String, dynamic>{'mode': prune ? 'prune' : 'stop', 'max_age_days': a, 'max_entries': n, 'max_bytes': b});
          }, child: const Text('预览影响')),
        ],
      )),
    );
  } finally {
    age.dispose(); count.dispose(); bytes.dispose();
  }
  if (policy == null || !context.mounted || session.readOnly) return;
  await guarded(context, () async {
    final plan = mapOf(await session.command({'op': 'history.retention.preview', 'policy': policy}));
    if (!context.mounted || session.readOnly) return;
    final automatic = policy!['mode'] == 'prune';
    final accepted = await confirm(context, '确认整个历史库策略？',
      '将永久删除 ${plan['delete_entries']} 条记录（${historyBytes(plan['delete_bytes'])}），'
      '保留 ${plan['keep_entries']} 条。\n'
      '${automatic ? '此操作无法撤销，且会对所有集合开启后续自动永久清理。请先导出需要保留的数据。' : '不会删除已有记录；达到限额时暂停未来历史记录。'}',
      action: automatic ? '确认删除并启用' : '保存限制');
    if (!accepted || !context.mounted || session.readOnly) return;
    await session.command({'op': 'history.retention.apply', 'token': plan['token'], 'confirmed': true});
  });
}

Future<void> compactHistoryStorage(BuildContext context, AppSession session) async {
  final accepted = await confirm(context, '整理整个历史数据库？',
    '仅回收空闲页，不删除现有记录。整理期间会阻止写入并可能需要额外临时磁盘空间；请等待完成。', action: '整理');
  if (!accepted || !context.mounted || session.readOnly) return;
  await guarded(context, () => session.command({'op': 'history.compact', 'confirmed': true}));
}
