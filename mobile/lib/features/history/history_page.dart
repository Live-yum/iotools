import 'package:flutter/material.dart';
import '../../core/json.dart';
import '../../core/session.dart';
import '../../shared/widgets.dart';

class HistoryPage extends StatefulWidget {
  const HistoryPage({
    required this.session,
    required this.onPrepare,
    super.key,
  });
  final AppSession session;
  final ValueChanged<JsonMap> onPrepare;
  @override
  State<HistoryPage> createState() => _HistoryPageState();
}

class _HistoryPageState extends State<HistoryPage> {
  List<JsonMap> entries = [];
  String filter = '';
  bool loading = true;
  bool databaseExists = false;
  String? error;
  @override
  void initState() {
    super.initState();
    load();
  }

  Future<void> load() async {
    try {
      final status = mapOf(
        await widget.session.command({'op': 'history.status'}),
      );
      final r = rowsOf(await widget.session.command({'op': 'history.list'}));
      if (mounted)
        setState(() {
          entries = r;
          databaseExists = status['exists'] == true;
          loading = false;
          error = null;
        });
    } catch (e) {
      if (mounted)
        setState(() {
          error = '$e';
          loading = false;
        });
    }
  }

  @override
  Widget build(BuildContext context) => RefreshIndicator(
    onRefresh: load,
    child: ListView(
      padding: const EdgeInsets.all(16),
      children: [
        if (!widget.session.history)
          const Card(
            child: Padding(
              padding: EdgeInsets.all(16),
              child: Text('HTTP 历史当前关闭。可在设置中明确开启；查看历史不会创建数据库。'),
            ),
          ),
        ActionWrap(
          padding: const EdgeInsets.only(bottom: 12),
          children: [
            OutlinedButton.icon(
              onPressed: load,
              icon: const Icon(Icons.refresh),
              label: const Text('刷新'),
            ),
            OutlinedButton(
              onPressed: databaseExists && !loading
                  ? () => sqlDialog(context, widget.session)
                  : null,
              child: const Text('SQL 查询 / 事务'),
            ),
            OutlinedButton(
              onPressed: databaseExists && !loading
                  ? () => collections()
                  : null,
              child: const Text('集合管理'),
            ),
          ],
        ),
        if (!loading && !databaseExists)
          const Padding(
            padding: EdgeInsets.only(bottom: 12),
            child: Text('暂无历史数据库，SQL 与集合管理暂不可用。开启 HTTP 历史并执行请求后即可使用。'),
          ),
        TextField(
          decoration: const InputDecoration(labelText: '筛选请求 / 状态 / 时间'),
          onChanged: (v) => setState(() => filter = v),
        ),
        if (loading) const LinearProgressIndicator(),
        if (error != null)
          Text(error!, style: const TextStyle(color: Colors.redAccent)),
        if (!loading && entries.isEmpty)
          const EmptyState('暂无执行历史', '运行 HTTP 请求且明确开启历史后，记录会出现在这里'),
        ...entries
            .where(
              (e) => e.toString().toLowerCase().contains(filter.toLowerCase()),
            )
            .map(
              (e) => Card(
                child: ListTile(
                  title: Text(
                    '${e['method'] ?? ''} ${e['recipe'] ?? e['request_id'] ?? ''}',
                  ),
                  subtitle: Text(
                    '${e['created_at'] ?? ''}\n状态 ${e['status']} · ${e['body_bytes'] ?? 0} 字节',
                  ),
                  isThreeLine: true,
                  onTap: () => detail(e),
                  trailing: IconButton(
                    tooltip: '删除历史记录',
                    onPressed: widget.session.readOnly ? null : () => remove(e),
                    icon: const Icon(Icons.delete_outline),
                  ),
                ),
              ),
            ),
      ],
    ),
  );
  Future<void> detail(JsonMap row) async {
    await guarded(context, () async {
      final e = mapOf(
        await widget.session.command({
          'op': 'history.get',
          'history_id': row['id'],
        }),
      );
      if (!mounted) return;
      await memoryDialog(
        context: context,
        builder: (c) => AlertDialog(
          title: const Text('HTTP 历史详情'),
          content: SizedBox(
            width: 700,
            child: SingleChildScrollView(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  SelectableText('${e['created_at'] ?? ''}\n状态 ${e['status']}'),
                  const SizedBox(height: 12),
                  const Text('正文'),
                  SelectableText(historyText(e, 'body')),
                  if (e['transformed'] != null) ...[
                    const Text('转换后正文'),
                    SelectableText(historyText(e, 'transformed')),
                  ],
                  const Text('响应头'),
                  DataView(e['headers']),
                  if (e['raw_body_base64'] != null)
                    ExpansionTile(
                      title: const Text('原始二进制（Base64）'),
                      children: [
                        SelectableText(e['raw_body_base64'].toString()),
                      ],
                    ),
                ],
              ),
            ),
          ),
          actions: [
            TextButton(
              onPressed: () =>
                  showData(c, '历史原始记录', e, export: widget.session.exportText),
              child: const Text('复制 / 导出'),
            ),
            TextButton(
              onPressed: () => Navigator.pop(c),
              child: const Text('关闭'),
            ),
          ],
        ),
      );
    });
  }

  Future<void> remove(JsonMap e) async {
    if (await confirm(
          context,
          '删除历史记录？',
          '删除本地记录 ${e['id']}，无法撤销。',
          action: '删除',
        ) &&
        mounted) {
      await guarded(
        context,
        () => widget.session.command({
          'op': 'history.delete',
          'ids': [e['id']],
          'confirmed': true,
        }),
      );
      await load();
    }
  }

  Future<void> collections() async {
    await guarded(context, () async {
      final rows = rowsOf(
        await widget.session.command({'op': 'history.collections'}),
      );
      if (!mounted) return;
      await memoryDialog(
        context: context,
        builder: (c) => AlertDialog(
          title: const Text('历史集合'),
          content: SizedBox(
            width: 600,
            child: SingleChildScrollView(
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  for (final r in rows)
                    ListTile(
                      title: Text(r['collection'].toString()),
                      subtitle: Text('${r['requests']} 条请求'),
                      trailing: PopupMenuButton<String>(
                        enabled: !widget.session.readOnly,
                        onSelected: (kind) async {
                          String? target;
                          if (kind == 'rename' ||
                              kind == 'merge' ||
                              kind == 'migrate')
                            target = await inputDialog(
                              c,
                              kind == 'rename'
                                  ? '新集合名称'
                                  : kind == 'merge'
                                  ? '合并到集合'
                                  : '迁移到集合',
                            );
                          if ((kind == 'rename' ||
                                  kind == 'merge' ||
                                  kind == 'migrate') &&
                              target == null)
                            return;
                          if (!c.mounted) return;
                          await guarded(c, () async {
                            final p = mapOf(
                              await widget.session.command({
                                'op': 'history.collection.preview',
                                'kind': kind,
                                'source': r['collection'],
                                if (target != null) 'target': target,
                              }),
                            );
                            if (c.mounted)
                              await executeHistory(c, widget.session, p);
                          });
                        },
                        itemBuilder: (c) => const [
                          PopupMenuItem(value: 'rename', child: Text('重命名')),
                          PopupMenuItem(value: 'merge', child: Text('合并')),
                          PopupMenuItem(value: 'delete', child: Text('删除')),
                          PopupMenuItem(value: 'migrate', child: Text('迁移')),
                        ],
                      ),
                    ),
                ],
              ),
            ),
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(c),
              child: const Text('关闭'),
            ),
          ],
        ),
      );
    });
  }
}

String historyText(JsonMap e, String key) => e[key]?.toString() ?? '';
Future<void> sqlDialog(BuildContext context, AppSession session) async {
  final sql = TextEditingController(text: 'SELECT * FROM requests LIMIT 100');
  Object? result;
  bool busy = false;
  await memoryDialog(
    context: context,
    builder: (c) => StatefulBuilder(
      builder: (c, set) => AlertDialog(
        title: const Text('历史 SQL'),
        content: SizedBox(
          width: 850,
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                TextField(
                  key: const ValueKey('sql_editor'),
                  controller: sql,
                  minLines: 4,
                  maxLines: 10,
                  decoration: const InputDecoration(labelText: 'SQL'),
                ),
                if (busy) const LinearProgressIndicator(),
                if (result != null) DataView(result),
              ],
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(c),
            child: const Text('关闭'),
          ),
          OutlinedButton(
            onPressed: busy || session.readOnly
                ? null
                : () => guarded(c, () async {
                    final p = mapOf(
                      await session.command({
                        'op': 'history.preview',
                        'sql': sql.text,
                      }),
                    );
                    if (c.mounted) await executeHistory(c, session, p);
                  }),
            child: const Text('预览变更事务'),
          ),
          FilledButton(
            key: const ValueKey('sql_query'),
            onPressed: busy
                ? null
                : () => guarded(c, () async {
                    set(() => busy = true);
                    try {
                      final r = await session.command({
                        'op': 'history.query',
                        'sql': sql.text,
                      });
                      if (c.mounted) set(() => result = r);
                    } finally {
                      if (c.mounted) set(() => busy = false);
                    }
                  }),
            child: const Text('只读查询'),
          ),
        ],
      ),
    ),
  );
  sql.dispose();
}

Future<void> executeHistory(
  BuildContext context,
  AppSession session,
  JsonMap preview,
) async {
  if (!await confirm(
        context,
        '审阅数据库变更',
        '${preview['sql']}\n\n将先创建 ${preview['backup_bytes']} 字节的独立备份；当前数据库必须与预览完全一致。',
        action: '选择备份并继续',
      ) ||
      !context.mounted)
    return;
  final backup = await inputDialog(
    context,
    '新备份文件名',
    initial: 'history_backup_${DateTime.now().millisecondsSinceEpoch}.sqlite',
  );
  if (backup == null || !context.mounted) return;
  final r = await session.command({
    'op': 'history.execute',
    'sql': preview['sql'],
    'token': preview['token'],
    'backup': backup,
    'confirmed': true,
  });
  if (context.mounted) showData(context, '事务完成', r, export: session.exportText);
}
