import 'package:flutter/material.dart';
import '../../core/json.dart';
import '../../core/session.dart';

/// Retains one page only; the complete tree stays in the bounded native cache.
class OpcuaScanResultPage extends StatefulWidget {
  const OpcuaScanResultPage({
    required this.session,
    required this.runId,
    super.key,
  });
  final AppSession session;
  final String runId;
  @override
  State<OpcuaScanResultPage> createState() => _OpcuaScanResultPageState();
}

class _OpcuaScanResultPageState extends State<OpcuaScanResultPage> {
  JsonMap descriptor = {};
  List<JsonMap> nodes = [];
  final previous = <int>[];
  final scroll = ScrollController();
  int offset = 0, next = 0, generation = 0;
  bool loading = true;
  String? error;

  @override
  void initState() {
    super.initState();
    load(0);
  }

  @override
  void dispose() {
    scroll.dispose();
    super.dispose();
  }

  Future<void> load(
    int target, {
    bool forward = false,
    bool back = false,
  }) async {
    final ticket = ++generation;
    final session = widget.session, epoch = widget.session.epoch;
    bool current() =>
        identical(session, widget.session) &&
        !session.disposed &&
        session.epoch == epoch &&
        session.resultRunId == widget.runId;
    setState(() {
      loading = true;
      error = null;
    });
    try {
      if (!current()) throw StateError('扫描会话已变化，请重新打开');
      final page = mapOf(
        await widget.session.command({
          'op': 'opcua.scan.page',
          'run_id': widget.runId,
          'offset': target,
          'limit': 100,
        }),
      );
      if (!mounted || ticket != generation) return;
      if (!current()) throw StateError('扫描会话已变化，请重新打开');
      if (page['run_id'] != widget.runId || page['offset'] != target) {
        throw StateError('扫描结果已失效，请重新打开');
      }
      final rows = rowsOf(page['nodes']);
      final newNext = (page['next_offset'] as num).toInt();
      final scan = mapOf(page['scan_result']);
      if (rows.length > 100 ||
          newNext != target + rows.length ||
          scan['run_id'] != widget.runId ||
          newNext > (scan['node_count'] as num).toInt()) {
        throw StateError('扫描分页不一致，请重新打开');
      }
      setState(() {
        if (forward) previous.add(offset);
        if (back) previous.removeLast();
        offset = target;
        next = newNext;
        nodes = rows;
        descriptor = scan;
      });
      if (scroll.hasClients) scroll.jumpTo(0);
    } catch (failure) {
      if (mounted && ticket == generation) {
        setState(() {
          error = '$failure';
          nodes = [];
          descriptor = {};
          previous.clear();
          next = offset;
        });
      }
    } finally {
      if (mounted && ticket == generation) setState(() => loading = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    final total = (descriptor['node_count'] as num?)?.toInt() ?? 0;
    final state = error != null
        ? '扫描结果不可用'
        : descriptor['finished'] != true
        ? '扫描中'
        : descriptor['complete'] == true
        ? '完整扫描'
        : '部分结果，扫描未完成';
    return Scaffold(
      appBar: AppBar(title: const Text('OPC UA 扫描结果')),
      body: Column(
        children: [
          Flexible(
            flex: 2,
            child: SingleChildScrollView(
              key: const ValueKey('scan_status_scroll'),
              child: Column(
                children: [
                  Text(
                    error != null ? state : '$state · 已保留 $total 个节点',
                    key: const ValueKey('scan_count'),
                  ),
                  const Text('节点和父节点来自完整缓存；执行日志可独立淘汰。新执行或关闭会清除缓存。扫描中可刷新当前页。'),
                  if ('${descriptor['error'] ?? ''}'.isNotEmpty)
                    Text(
                      '${descriptor['error']}',
                      key: const ValueKey('scan_failure'),
                    ),
                  if (error != null)
                    Text(error!, key: const ValueKey('scan_page_error')),
                ],
              ),
            ),
          ),
          if (loading) const LinearProgressIndicator(),
          Wrap(
            alignment: WrapAlignment.center,
            crossAxisAlignment: WrapCrossAlignment.center,
            children: [
              TextButton(
                key: const ValueKey('scan_previous'),
                onPressed: loading || previous.isEmpty
                    ? null
                    : () => load(previous.last, back: true),
                child: const Text('上一页'),
              ),
              Text('${nodes.isEmpty ? 0 : offset + 1}–$next / $total'),
              TextButton(
                key: const ValueKey('scan_next'),
                onPressed: loading || error != null || next >= total
                    ? null
                    : () => load(next, forward: true),
                child: const Text('下一页'),
              ),
              IconButton(
                tooltip: '刷新当前页',
                onPressed: loading ? null : () => load(offset),
                icon: const Icon(Icons.refresh),
              ),
            ],
          ),
          Expanded(
            flex: 3,
            child: ListView.builder(
              key: const ValueKey('scan_nodes'),
              controller: scroll,
              itemCount: nodes.length,
              itemBuilder: (context, index) {
                final node = nodes[index];
                return ListTile(
                  title: Text(
                    '${node['display_name']} · ${node['node_class']}',
                  ),
                  subtitle: SelectableText(
                    '${node['node_id']}\n父节点: ${node['parent_id']}\n'
                    '浏览名: ${node['browse_name']} · 深度: ${node['depth']}',
                  ),
                );
              },
            ),
          ),
        ],
      ),
    );
  }
}
