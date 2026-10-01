import 'package:flutter/material.dart';
import '../../core/json.dart';

class RetainedPreview extends StatefulWidget {
  const RetainedPreview({
    required this.snapshot,
    required this.request,
    required this.onPrepare,
    super.key,
  });
  final JsonMap snapshot, request;
  final ValueChanged<JsonMap> onPrepare;
  @override
  State<RetainedPreview> createState() => _RetainedPreviewState();
}

class _RetainedPreviewState extends State<RetainedPreview> {
  String search = '';
  int page = 0;
  @override
  Widget build(BuildContext context) {
    final all = widget.snapshot['topics'] as List? ?? [],
        shown = all.where((v) => v.toString().contains(search)).toList();
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(
          '${all.length} 个精确主题 · ${widget.snapshot['scan_duration_ms']}ms 有界扫描',
        ),
        Text(
          widget.snapshot['warning']?.toString() ??
              '清理前请暂停其他 retained 发布者，逐项完成可能被中断。',
        ),
        TextField(
          decoration: const InputDecoration(labelText: '搜索完整预览主题（仅筛选显示）'),
          onChanged: (v) => setState(() {
            search = v;
            page = 0;
          }),
        ),
        for (final topic in shown.skip(page * 30).take(30))
          SelectableText(topic.toString()),
        Row(
          children: [
            Text('${shown.length} 项 · 第 ${page + 1} 页'),
            const Spacer(),
            IconButton(
              tooltip: '上一页主题',
              onPressed: page > 0 ? () => setState(() => page--) : null,
              icon: const Icon(Icons.chevron_left),
            ),
            IconButton(
              tooltip: '下一页主题',
              onPressed: (page + 1) * 30 < shown.length
                  ? () => setState(() => page++)
                  : null,
              icon: const Icon(Icons.chevron_right),
            ),
          ],
        ),
        OutlinedButton(
          onPressed: all.isEmpty
              ? null
              : () {
                  final request = cloneMap(widget.request);
                  request['action'] = 'clean-retained';
                  request['params'] = {
                    ...mapOf(request['params']),
                    'confirm_topics': List.of(all),
                    'confirm_token': widget.snapshot['confirm_token'],
                  };
                  widget.onPrepare(request);
                },
          child: Text('准备清理完整快照（${all.length} 个精确主题）'),
        ),
      ],
    );
  }
}
