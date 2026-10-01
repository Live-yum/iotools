import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'modbus_host.dart';
import 'modbus_models.dart';
import 'modbus_widgets.dart';

/// Only sanitized observer metadata enters this operational log; protocol values,
/// raw errors, target credentials and payloads stay in their dedicated views.
class ModbusActivityView extends StatefulWidget {
  const ModbusActivityView({super.key, required this.host});
  final ModbusHost host;
  @override
  State<ModbusActivityView> createState() => _ModbusActivityViewState();
}

class _ModbusActivityViewState extends State<ModbusActivityView> {
  final List<Map<String, dynamic>> log = [];
  final Set<String> seen = {};
  final ScrollController scroll = ScrollController();
  bool follow = true, wrap = true;
  int evicted = 0;
  @override
  void initState() {
    super.initState();
    sync();
    widget.host.addListener(sync);
  }

  void sync() {
    for (final event in widget.host.events) {
      if (event['kind'] != 'modbus.metrics') continue;
      final id = '${event['run_id']}:${event['seq']}';
      if (!seen.add(id)) continue;
      final data = mbMap(event['data']);
      log.add({
        'run_id': event['run_id'],
        for (final key in [
          'time',
          'action',
          'unit',
          'address',
          'count',
          'duration_ms',
          'write',
          'success',
          'cancelled',
          'error_class',
        ])
          if (data.containsKey(key)) key: data[key],
      });
      while (log.length > 200) {
        log.removeAt(0);
        evicted++;
      }
      while (seen.length > 1024) {
        seen.remove(seen.first);
      }
    }
    if (mounted) setState(() {});
    if (follow)
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted && scroll.hasClients)
          scroll.jumpTo(scroll.position.maxScrollExtent);
      });
  }

  @override
  void dispose() {
    widget.host.removeListener(sync);
    scroll.dispose();
    super.dispose();
  }

  String get text => log.map(jsonEncode).join('\n');
  @override
  Widget build(BuildContext context) => mbCard('本机会话活动', [
    Text('保留 ${log.length} / 200 项 · 淘汰 $evicted 项 · 不记录负载或凭据'),
    Wrap(
      spacing: 8,
      children: [
        FilterChip(
          label: const Text('跟随'),
          selected: follow,
          onSelected: (v) => setState(() => follow = v),
        ),
        FilterChip(
          label: const Text('换行'),
          selected: wrap,
          onSelected: (v) => setState(() => wrap = v),
        ),
      ],
    ),
    SizedBox(
      height: 260,
      child: ListView.builder(
        controller: scroll,
        itemCount: log.length,
        itemBuilder: (_, i) => Padding(
          padding: const EdgeInsets.symmetric(vertical: 6),
          child: wrap
              ? SelectableText(jsonEncode(log[i]))
              : SingleChildScrollView(
                  scrollDirection: Axis.horizontal,
                  child: SelectableText(jsonEncode(log[i])),
                ),
        ),
      ),
    ),
    Wrap(
      spacing: 8,
      children: [
        OutlinedButton(
          onPressed: () => Clipboard.setData(ClipboardData(text: text)),
          child: const Text('复制活动'),
        ),
        OutlinedButton(
          onPressed: () => widget.host.exportText('modbus-session.jsonl', text),
          child: const Text('导出活动'),
        ),
        OutlinedButton(
          onPressed: () async {
            if (await mbConfirm(context, '清除本机活动视图', [
              const Text('只清除当前日志视图，累计统计与持久写入审计仍保留。'),
            ], confirm: '清除'))
              setState(() {
                log.clear();
                evicted = 0;
              });
          },
          child: const Text('清除活动'),
        ),
      ],
    ),
  ]);
}
