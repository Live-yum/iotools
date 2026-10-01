import 'dart:convert';
import 'package:flutter/material.dart';
import '../core/json.dart';
import 'widgets.dart';

Future<bool> reviewExecution(BuildContext context, JsonMap preview) async =>
    await memoryDialog<bool>(
      context: context,
      builder: (c) => _ExecutionReview(preview),
    ) ??
    false;

class _ExecutionReview extends StatefulWidget {
  const _ExecutionReview(this.preview);
  final JsonMap preview;
  @override
  State<_ExecutionReview> createState() => _ExecutionReviewState();
}

class _ExecutionReviewState extends State<_ExecutionReview>
    with WidgetsBindingObserver {
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if ((state == AppLifecycleState.paused ||
            state == AppLifecycleState.detached) &&
        mounted)
      Navigator.pop(context, false);
  }

  @override
  Widget build(BuildContext context) {
    final preview = widget.preview,
        request = safePreviewRequest(mapOf(preview['request'])),
        protocol = mapOf(preview['protocol_preview']);
    return AlertDialog(
      title: const Text('确认执行写操作'),
      content: SizedBox(
        width: 660,
        child: SingleChildScrollView(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            mainAxisSize: MainAxisSize.min,
            children: [
              Text(
                preview['summary']?.toString() ?? '',
                style: Theme.of(context).textTheme.titleMedium,
              ),
              const SizedBox(height: 12),
              _value('目标地址', request['endpoint']),
              _value(
                '协议 / 操作',
                '${request['protocol']} / ${request['action']}',
              ),
              if (protocol.isNotEmpty) ...[
                for (final e in protocol.entries)
                  _value(
                    const {
                          'unit': '设备单元',
                          'function_code': '功能码',
                          'address': '写入起始地址',
                          'count': '数量',
                          'read_address': 'FC23 读取起始地址',
                          'read_count': 'FC23 读取数量',
                          'registers': '精确编码寄存器 (registers)',
                          'coils': '线圈状态 (coils)',
                          'word_order': '字节与字顺序',
                          'pdu_hex': '原始 PDU',
                        }[e.key] ??
                        e.key,
                    e.value,
                  ),
              ] else
                ...reviewParameters(context, request),
              for (final warning in preview['warnings'] as List? ?? [])
                Padding(
                  padding: const EdgeInsets.only(top: 8),
                  child: Text(
                    warning.toString(),
                    style: const TextStyle(color: Colors.amber),
                  ),
                ),
              const SizedBox(height: 12),
              const Text('取消不会发送写操作。完成后不会自动重试。'),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context, false),
          child: const Text('取消'),
        ),
        FilledButton(
          key: const ValueKey('confirm_action'),
          onPressed: () => Navigator.pop(context, true),
          child: const Text('确认执行'),
        ),
      ],
    );
  }

  Widget _value(String label, Object? value) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 5),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text(label, style: Theme.of(context).textTheme.labelMedium),
        SelectableText(
          value is Map || value is List
              ? pretty(value)
              : value?.toString() ?? '—',
        ),
      ],
    ),
  );
}

/// Presentation only: retain source request and token exactly as returned by Go.
JsonMap safePreviewRequest(JsonMap source) {
  bool secret(String key) {
    final k = key.toLowerCase().replaceAll(RegExp(r'[_-]'), '');
    return [
          'password',
          'passwd',
          'token',
          'accesstoken',
          'refreshtoken',
          'bearer',
          'authorization',
          'proxyauthorization',
          'cookie',
          'setcookie',
          'secret',
          'clientsecret',
          'apikey',
          'xapikey',
          'iv',
        ].contains(k) ||
        k.endsWith('password') ||
        k.endsWith('token') ||
        k.endsWith('secret') ||
        k.endsWith('bearer');
  }

  Object? safe(Object? value) => value is Map
      ? value.map(
          (k, v) => MapEntry(
            k.toString(),
            secret(k.toString())
                ? '••••••'
                : k == 'crypto'
                ? '已隐藏密钥定义'
                : safe(v),
          ),
        )
      : value is List
      ? value.map(safe).toList()
      : value;
  final result = mapOf(safe(source));
  result['endpoint'] = redactedEndpoint(source['endpoint']?.toString() ?? '');
  return result;
}

List<Widget> reviewParameters(BuildContext context, JsonMap request) {
  final params = mapOf(request['params']);
  final widgets = <Widget>[];
  final shown = <String>{};
  final labels = {
    'body': '文本正文',
    'json': 'JSON 正文',
    'form_urlencoded': 'URL 编码表单',
    'form_multipart': 'Multipart 表单',
    'body_file': '流式正文文件',
    'body_stream': '流式正文模板',
    'payload': '发布内容',
    'topic': '目标主题',
    'topics': '目标主题列表',
    'node_id': '目标 NodeId',
    'node_ids': '目标节点',
    'method_id': '方法',
    'object_id': '对象',
    'subject': 'Schema Subject',
    'version': 'Schema 版本',
    'connector': 'Connector',
    'group': '消费组',
    'groups': '消费组列表',
    'partitions': '目标分区数',
    'confirm_topics': '精确清理主题',
  };
  for (final entry in labels.entries) {
    if (!params.containsKey(entry.key)) continue;
    shown.add(entry.key);
    final value = params[entry.key],
        text = value is String ? value : pretty(value);
    final bytes = utf8
        .encode(value is String ? value : exactEncode(value))
        .length;
    final content = SelectableText(text, key: ValueKey('review_${entry.key}'));
    widgets.add(
      Padding(
        padding: const EdgeInsets.symmetric(vertical: 8),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            Text(
              '${entry.value} · $bytes 字节',
              style: Theme.of(context).textTheme.labelLarge,
            ),
            if (text.length <= 2048)
              content
            else
              ExpansionTile(
                title: Text('${text.substring(0, 160)}…'),
                subtitle: const Text('展开完整内容'),
                children: [
                  SizedBox(
                    height: 260,
                    child: SingleChildScrollView(child: content),
                  ),
                ],
              ),
          ],
        ),
      ),
    );
  }
  if (request['protocol'] == 'http' &&
      !shown.any(
        (k) => [
          'body',
          'json',
          'form_urlencoded',
          'form_multipart',
          'body_file',
          'body_stream',
        ].contains(k),
      ))
    widgets.add(const Text('无请求正文'));
  if (params['headers'] != null) {
    shown.add('headers');
    widgets.add(
      ExpansionTile(
        title: const Text('请求头（凭据已隐藏）'),
        children: [DataView(params['headers'])],
      ),
    );
  }
  final other = {
    for (final e in params.entries)
      if (!shown.contains(e.key)) e.key: e.value,
  };
  if (other.isNotEmpty)
    widgets.add(
      ExpansionTile(title: const Text('其余参数与转换'), children: [DataView(other)]),
    );
  return widgets;
}
