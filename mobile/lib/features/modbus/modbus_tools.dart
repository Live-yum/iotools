import 'dart:convert';
import 'package:flutter/material.dart';
import 'modbus_controller.dart';
import 'modbus_host.dart';
import 'modbus_models.dart';
import 'modbus_widgets.dart';

class ModbusTools {
  ModbusTools(this.host, this.model, this.context);
  final ModbusHost host;
  final ModbusController model;
  final BuildContext Function() context;
  String discoveryRun = '', controllerRun = '';
  int discoveryPort = 502;
  Future<void> form(
    String title,
    List<Widget> children,
    Future<bool> Function() submit, {
    String confirm = '预览',
  }) => mbDialog<void>(
    context: context(),
    builder: (_) => ModbusFormDialog(
      title: title,
      children: children,
      confirm: confirm,
      onSubmit: submit,
    ),
  );
  Future<void> show(String title, Object? data) =>
      mbShow(context(), title, mbStructured(data));
  Future<void> sourceFromPrivate(TextEditingController target) async {
    final files = mbRows(await host.command({'op': 'files.list'}));
    if (!context().mounted) return;
    final selected = await mbDialog<String>(
      context: context(),
      builder: (ctx) => AlertDialog(
        title: const Text('选择已导入的私有文本文件'),
        content: SizedBox(
          width: 560,
          height: 360,
          child: ListView.builder(
            itemCount: files.length,
            itemBuilder: (_, i) => ListTile(
              title: Text(
                files[i]['name']?.toString() ?? files[i]['path'].toString(),
              ),
              subtitle: Text('${files[i]['bytes']} 字节'),
              onTap: () => Navigator.pop(ctx, files[i]['path'].toString()),
            ),
          ),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('取消'),
          ),
        ],
      ),
    );
    if (selected == null) return;
    final data = mbMap(
      await host.command({'op': 'file.read', 'path': selected}),
    );
    final text =
        data['text']?.toString() ??
        utf8.decode(base64Decode(data['base64'].toString()));
    target.text = text;
  }

  List<Widget> sourceFields(TextEditingController source, String label) => [
    const Text('可粘贴 JSON/CSV，或选择通过系统文件页导入的私有文本文件。取消保持原草稿。'),
    OutlinedButton(
      onPressed: () async {
        try {
          await sourceFromPrivate(source);
        } catch (error) {
          if (context().mounted) await show('读取失败', error.toString());
        }
      },
      child: const Text('选择私有源文件'),
    ),
    mbField(label, source, multiline: true),
  ];
  Future<void> importAnnotations() async {
    final source = TextEditingController();
    final original = mbClone(model.draft),
        action = readAction(model.draft['action'].toString());
    await form(
      '导入 ${registerSpace(action)} 空间注释',
      sourceFields(source, '寄存器 JSON'),
      () async {
        final incoming = mbMap(
          await host.command({
            'op': 'modbus.registers.import',
            'source': source.text,
            'kind': action,
          }),
        );
        if (!context().mounted) return false;
        final p = mbMap(original['params']);
        final oldLabels = {
          for (final e in mbMap(p['labels']).entries)
            boundedInt(e.key, 0, 65535, '标签地址').toString(): e.value,
        };
        final newLabels = {
          for (final e in mbMap(incoming['labels']).entries)
            boundedInt(e.key, 0, 65535, '标签地址').toString(): e.value,
        };
        final oldRules = ruleMap(p['rules']),
            newRules = ruleMap(incoming['rules']);
        final labelConflicts = newLabels.keys
            .where(
              (a) => oldLabels.containsKey(a) && oldLabels[a] != newLabels[a],
            )
            .toList();
        final ruleConflicts = newRules.keys
            .where(
              (a) =>
                  oldRules.containsKey(a) &&
                  jsonEncode(oldRules[a]) != jsonEncode(newRules[a]),
            )
            .toList();
        final replaceLabels = <int>{}, replaceRules = <int>{};
        final applied = await mbDialog<bool>(
          context: context(),
          builder: (ctx) => StatefulBuilder(
            builder: (_, refresh) => AlertDialog(
              title: const Text('同空间合并预览'),
              content: SizedBox(
                width: 620,
                child: SingleChildScrollView(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      mbPair('空间', registerSpace(action)),
                      mbPair(
                        '固定地址并集',
                        mbMap(
                          mergeAnnotations(
                            original,
                            action,
                            incoming,
                          )['params'],
                        )['pins'],
                      ),
                      Text(
                        '新增或匹配标签 ${newLabels.length}，规则 ${newRules.length}。其他地址及连接参数保持原值。',
                      ),
                      const Text('冲突默认保留现值。只勾选明确允许替换的地址。'),
                      for (final key in labelConflicts)
                        CheckboxListTile(
                          title: Text(
                            '标签 $key：${oldLabels[key]} → ${newLabels[key]}',
                          ),
                          value: replaceLabels.contains(int.parse(key)),
                          onChanged: (v) => refresh(() {
                            if (v == true) {
                              replaceLabels.add(int.parse(key));
                            } else {
                              replaceLabels.remove(int.parse(key));
                            }
                          }),
                        ),
                      for (final key in ruleConflicts)
                        CheckboxListTile(
                          title: Text(
                            '规则 $key：${oldRules[key]?['repr']} → ${newRules[key]?['repr']}',
                          ),
                          value: replaceRules.contains(key),
                          onChanged: (v) => refresh(() {
                            if (v == true) {
                              replaceRules.add(key);
                            } else {
                              replaceRules.remove(key);
                            }
                          }),
                        ),
                    ],
                  ),
                ),
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(ctx, false),
                  child: const Text('取消'),
                ),
                FilledButton(
                  onPressed: () => Navigator.pop(ctx, true),
                  child: const Text('校验并应用草稿'),
                ),
              ],
            ),
          ),
        );
        if (applied != true) return false;
        if (jsonEncode(model.draft) != jsonEncode(original))
          throw const FormatException('请求草稿已变化，请重新导入预览');
        final merged = mergeAnnotations(
          original,
          action,
          incoming,
          overwriteLabels: replaceLabels,
          overwriteRules: replaceRules,
        );
        await host.command({
          'op': 'modbus.registers.export',
          'request': merged,
        });
        model.edit(merged);
        await model.reinterpret();
        return true;
      },
    );
    source.dispose();
  }

  Future<void> exportAnnotations() async {
    final data = mbMap(
      await host.command({
        'op': 'modbus.registers.export',
        'request': mbClone(model.draft),
      }),
    );
    await host.exportText(
      'modbus-${registerSpace(model.draft['action'].toString())}-registers.json',
      data['source'].toString(),
    );
  }

  Future<void> importMTUI() async {
    final source = TextEditingController(),
        prefix = TextEditingController(text: 'imported');
    await form(
      '完整 MTUI 配置转换',
      [
        mbField('新请求 ID 前缀', prefix),
        ...sourceFields(source, 'MTUI 配置 JSON'),
        const Text('转换仅生成四种空间的只读请求。API、自动采样循环、next_config 与写入权限不会激活。'),
      ],
      () async {
        final plan = mbMap(
          await host.command({
            'op': 'modbus.import',
            'source': source.text,
            'prefix': prefix.text.trim(),
          }),
        );
        final requests = mbRows(plan['requests']), selected = <String>{};
        if (!context().mounted) return false;
        await mbDialog<void>(
          context: context(),
          builder: (ctx) => StatefulBuilder(
            builder: (_, refresh) => ModbusFormDialog(
              title: '选择新增请求 · 尚未保存',
              confirm: '备份并追加到本机集合',
              children: [
                for (final warning in plan['warnings'] as List? ?? [])
                  Padding(
                    padding: const EdgeInsets.symmetric(vertical: 4),
                    child: Text('提示：$warning'),
                  ),
                for (final request in requests)
                  CheckboxListTile(
                    title: Text('${request['id']} · ${request['action']}'),
                    subtitle: Text(
                      '${request['endpoint']}\n单元 ${mbMap(request['params'])['unit']} · 地址 ${mbMap(request['params'])['address']} · 数量 ${mbMap(request['params'])['count']}',
                    ),
                    value: selected.contains(request['id']),
                    onChanged: (v) => refresh(() {
                      if (v == true) {
                        selected.add(request['id'].toString());
                      } else {
                        selected.remove(request['id']);
                      }
                    }),
                  ),
                const Text('明确保存只追加选中请求。引擎先创建原始集合备份，并拒绝重复 ID、源文件变化或过期预览。'),
              ],
              onSubmit: () async {
                if (host.readOnly) throw const FormatException('只读模式禁止保存');
                if (selected.isEmpty) throw const FormatException('至少选择一个新增请求');
                final result = mbMap(
                  await host.command({
                    'op': 'modbus.import.apply',
                    'token': plan['token'],
                    'request_ids': selected.toList(),
                    'confirmed': true,
                  }),
                );
                await host.collectionSaved(result);
                if (ctx.mounted)
                  await mbShow(ctx, '已保存选中请求', [
                    mbPair('原始集合备份', result['backup']),
                    Text('追加 ${selected.length} 个只读请求。'),
                  ]);
                return true;
              },
            ),
          ),
        );
        return true;
      },
      confirm: '生成转换预览',
    );
    source.dispose();
    prefix.dispose();
  }

  ModbusFrame requireFrame({bool registersOnly = false}) {
    final frame = model.current;
    if (frame == null || frame.gap || frame.words.isEmpty)
      throw const FormatException('没有来源完整的匹配响应，请先明确读取');
    if (registersOnly && !['holding', 'input'].contains(frame.source.space))
      throw const FormatException('快照仅支持 holding/input 寄存器');
    return frame;
  }

  Future<void> saveSnapshot() async {
    final frame = requireFrame(registersOnly: true),
        snapshot = requireFrame(registersOnly: true).snapshot();
    final path = TextEditingController(
      text: 'snapshot-${DateTime.now().millisecondsSinceEpoch}.json',
    );
    await form(
      '保存来源绑定快照',
      [
        mbPair(
          '实际端点 / 单元 / 空间',
          '${frame.source.endpoint} · ${frame.source.unit} · ${frame.source.space}',
        ),
        mbPair('run_id / 采样时间', '${frame.source.runId}\n${frame.time}'),
        mbPair('完整原始字数量', frame.words.length),
        mbField('新快照文件名', path),
        const Text('保存这一份已缓存响应。文件不可覆盖，不使用当前编辑目标来重命名数据来源。'),
      ],
      () async {
        final result = await host.command({
          'op': 'modbus.snapshot.save',
          'path': path.text.trim(),
          'snapshot': snapshot,
          'confirmed': true,
        });
        if (context().mounted) await show('快照保存结果', result);
        return true;
      },
      confirm: '保存新快照',
    );
    path.dispose();
  }

  Future<void> compareSnapshots() async {
    final before = TextEditingController(), after = TextEditingController();
    var cached = model.current != null;
    await form(
      '加载与比较快照',
      [
        mbField('基线私有文件路径', before),
        StatefulBuilder(
          builder: (_, refresh) => Column(
            children: [
              SwitchListTile(
                title: const Text('与当前来源绑定缓存比较'),
                value: cached,
                onChanged: (v) => refresh(() => cached = v),
              ),
              if (!cached) mbField('对比私有快照路径', after),
            ],
          ),
        ),
        const Text('端点、单元或寄存器空间不同会拒绝比较。缺失读数表示未读取，不等于设备删除。'),
      ],
      () async {
        final baseline = await host.command({
          'op': 'modbus.snapshot.load',
          'path': before.text.trim(),
        });
        final other = cached
            ? requireFrame(registersOnly: true).snapshot()
            : await host.command({
                'op': 'modbus.snapshot.load',
                'path': after.text.trim(),
              });
        final diff = await host.command({
          'op': 'modbus.snapshot.diff',
          'before': baseline,
          'after': other,
        });
        if (context().mounted) await _diff('快照变化', diff);
        return false;
      },
      confirm: '加载并比较',
    );
    before.dispose();
    after.dispose();
  }

  Future<void> csvDiff() async {
    final frame = requireFrame(), source = TextEditingController();
    var hex = false, acknowledged = false;
    await form(
      'CSV 离线差异',
      [
        ...sourceFields(source, 'CSV 文本'),
        mbPair(
          '缓存实际来源',
          '${frame.source.endpoint} · ${frame.source.unit} · ${frame.source.space}\n${frame.time}',
        ),
        StatefulBuilder(
          builder: (_, refresh) => Column(
            children: [
              CheckboxListTile(
                title: const Text('CSV 地址按十六进制解析'),
                value: hex,
                onChanged: (v) => refresh(() => hex = v ?? false),
              ),
              CheckboxListTile(
                title: const Text('我确认 CSV 属于此端点、单元及空间'),
                value: acknowledged,
                onChanged: (v) => refresh(() => acknowledged = v ?? false),
              ),
            ],
          ),
        ),
        const Text('CSV 自身没有可信目标元数据。未读取位置保持未知；最多展示 2000 行差异。'),
      ],
      () async {
        if (!acknowledged) throw const FormatException('先确认 CSV 的目标来源');
        final diff = await host.command({
          'op': 'modbus.csv.diff',
          'source': source.text,
          'kind': frame.source.space,
          'words': frame.words,
          'hex_address': hex,
        });
        if (context().mounted) await _diff('CSV 差异', diff);
        return false;
      },
      confirm: '比较本机缓存',
    );
    source.dispose();
  }

  Future<void> _diff(String title, Object? data) async {
    final rows = mbRows(data);
    var changedOnly = true;
    await mbDialog<void>(
      context: context(),
      builder: (ctx) => StatefulBuilder(
        builder: (_, refresh) {
          final visible = rows
              .where(
                (row) =>
                    !changedOnly ||
                    (row['before'] ?? row['Before']) !=
                        (row['after'] ?? row['After']),
              )
              .toList();
          return AlertDialog(
            title: Text(title),
            content: SizedBox(
              width: 620,
              height: 460,
              child: Column(
                children: [
                  SwitchListTile(
                    title: const Text('只看变化或未读取'),
                    value: changedOnly,
                    onChanged: (v) => refresh(() => changedOnly = v),
                  ),
                  Text(
                    '${visible.length} / ${rows.length} 项${visible.length > 2000 ? '，显示前 2000 项' : ''}',
                  ),
                  const Text('“未读取”是缺少本次样本，不表示设备值被删除。'),
                  Expanded(
                    child: ListView.builder(
                      itemCount: visible.length > 2000 ? 2000 : visible.length,
                      itemBuilder: (_, i) {
                        final row = visible[i];
                        return mbCard(
                          '地址 ${row['address'] ?? mbMap(row['Cell'])['Address']}',
                          [
                            mbPair('空间', mbMap(row['Cell'])['Type']),
                            mbPair(
                              '原值',
                              row['before'] ?? row['Before'] ?? '未读取',
                            ),
                            mbPair(
                              '本次值',
                              row['after'] ?? row['After'] ?? '未读取',
                            ),
                          ],
                        );
                      },
                    ),
                  ),
                ],
              ),
            ),
            actions: [
              OutlinedButton(
                onPressed: () => host.exportText(
                  'modbus-diff.json',
                  const JsonEncoder.withIndent('  ').convert(data),
                ),
                child: const Text('导出完整差异'),
              ),
              TextButton(
                onPressed: () => Navigator.pop(ctx),
                child: const Text('关闭'),
              ),
            ],
          );
        },
      ),
    );
  }

  Future<void> discovery() async {
    final target = TextEditingController(),
        port = TextEditingController(text: '$discoveryPort'),
        timeout = TextEditingController(text: '500'),
        concurrency = TextEditingController(text: '8');
    var method = 'tcp';
    await form(
      '明确网络探测范围',
      [
        const Text('IP 列表或对齐的 /24–/32，最多 254 个目标。开放端口不代表 Modbus 设备。'),
        mbField('IP 列表或 CIDR', target, multiline: true),
        StatefulBuilder(
          builder: (_, refresh) => mbSelect('探测方法', method, const [
            'tcp',
            'ping',
          ], (v) => refresh(() => method = v)),
        ),
        mbField('TCP 端口', port, numeric: true),
        mbField('超时 ms（100–2000）', timeout, numeric: true),
        mbField('并发（1–32）', concurrency, numeric: true),
      ],
      () async {
        final chosenPort = boundedInt(port.text, 1, 65535, '端口');
        final plan = mbMap(
          await host.command({
            'op': 'modbus.discovery.preview',
            'target': target.text.trim(),
            'port': chosenPort,
            'timeout_ms': boundedInt(timeout.text, 100, 2000, '超时'),
            'concurrency': boundedInt(concurrency.text, 1, 32, '并发'),
            'method': method,
          }),
        );
        if (!context().mounted) return false;
        if (!await mbConfirm(context(), '将联系以下全部目标', [
          mbPair('方法 / 端口', '${plan['method']} / ${plan['port']}'),
          mbPair(
            '超时 / 并发',
            '${plan['timeout_ms']} ms / ${plan['concurrency']}',
          ),
          Text(plan['notice'].toString()),
          for (final target in plan['targets'] as List? ?? [])
            Text(target.toString()),
        ], confirm: '开始有限探测'))
          return false;
        final result = mbMap(
          await host.command({
            'op': 'modbus.discovery.run',
            'token': plan['token'],
            'confirmed': true,
          }),
        );
        discoveryRun = result['run_id'].toString();
        discoveryPort = chosenPort;
        host.started(result);
        return true;
      },
      confirm: '展开全部目标',
    );
    for (final c in [target, port, timeout, concurrency]) {
      c.dispose();
    }
  }

  Future<void> discoveryResults() async {
    await mbDialog<void>(
      context: context(),
      builder: (ctx) => AnimatedBuilder(
        animation: host,
        builder: (_, __) {
          final events = host.events
              .where(
                (event) =>
                    event['kind'] == 'discovery' &&
                    (discoveryRun.isEmpty || event['run_id'] == discoveryRun),
              )
              .toList();
          return AlertDialog(
            title: const Text('网络探测结果'),
            content: SizedBox(
              width: 620,
              height: 440,
              child: ListView(
                children: [
                  const Text('仅选择目标到草稿，不会连接。开放端口不证明 Modbus 可用。'),
                  for (final event in events)
                    ListTile(
                      title: Text(mbMap(event['data'])['address'].toString()),
                      subtitle: Text(
                        mbMap(event['data'])['open'] == true
                            ? '连通'
                            : mbMap(event['data'])['error']?.toString() ??
                                  '未开放',
                      ),
                      trailing: OutlinedButton(
                        onPressed: () {
                          final hostAddress = mbMap(
                            event['data'],
                          )['address'].toString();
                          final next = mbClone(
                            model.draft,
                          )..['endpoint'] = 'tcp://$hostAddress:$discoveryPort';
                          model.edit(next);
                          host.prepare(next);
                        },
                        child: const Text('选择'),
                      ),
                    ),
                ],
              ),
            ),
            actions: [
              TextButton(
                onPressed: discoveryRun.isEmpty
                    ? null
                    : () async {
                        await host.command({
                          'op': 'cancel',
                          'run_id': discoveryRun,
                        });
                      },
                child: const Text('停止本次探测'),
              ),
              TextButton(
                onPressed: () => Navigator.pop(ctx),
                child: const Text('关闭'),
              ),
            ],
          );
        },
      ),
    );
  }

  Future<void> controller() async {
    final listen = TextEditingController(text: '127.0.0.1:18080'),
        unit = TextEditingController(
          text: (model.params['unit'] ?? 1).toString(),
        ),
        address = TextEditingController(
          text: (model.params['address'] ?? 0).toString(),
        ),
        count = TextEditingController(text: '1');
    var write = false, type = 'holding';
    await form(
      '本机 HTTP 控制服务',
      [
        mbField('数字回环监听地址', listen),
        StatefulBuilder(
          builder: (_, refresh) => Column(
            children: [
              SwitchListTile(
                title: const Text('开放有限写入范围'),
                value: write,
                onChanged: host.readOnly
                    ? null
                    : (v) => refresh(() => write = v),
              ),
              if (write) ...[
                mbSelect('可写空间', type, const [
                  'holding',
                  'coil',
                ], (v) => refresh(() => type = v)),
                mbField('可写单元', unit, numeric: true),
                mbField('可写起始地址', address, numeric: true),
                mbField('可写数量', count, numeric: true),
              ],
            ],
          ),
        ),
        const Text('默认只读，仅允许数字回环监听。运行期间可明确停止；离开应用会取消服务。'),
        if (controllerRun.isNotEmpty)
          OutlinedButton(
            onPressed: () async {
              await host.command({'op': 'cancel', 'run_id': controllerRun});
            },
            child: const Text('停止此服务'),
          ),
      ],
      () async {
        final value = listen.text.trim();
        final match = RegExp(
          r'^(127(?:\.\d{1,3}){3}|\[::1\]):(\d+)$',
        ).firstMatch(value);
        if (match == null)
          throw const FormatException('仅允许 127.x.x.x:端口 或 [::1]:端口');
        boundedInt(match.group(2), 1, 65535, '监听端口');
        final scope = write
            ? {
                'Unit': boundedInt(unit.text, 1, 247, '单元'),
                'Address': boundedInt(address.text, 0, 65535, '地址'),
                'Count': boundedInt(
                  count.text,
                  1,
                  type == 'coil' ? 1968 : 123,
                  '数量',
                ),
                'Type': type,
              }
            : null;
        if (scope != null &&
            (scope['Address'] as int) + (scope['Count'] as int) > 65536)
          throw const FormatException('写入范围越界');
        final request = mbClone(model.draft),
            preview = mbMap(
              await host.command({'op': 'preview', 'request': request}),
            );
        if (!context().mounted) return false;
        if (!await mbConfirm(context(), '启动本机服务', [
          mbPair('监听', value),
          mbPair('实际设备', mbMap(preview['request'])['endpoint']),
          mbPair('默认设备单元', mbMap(mbMap(preview['request'])['params'])['unit']),
          if (scope == null)
            const Text('只读服务，不开放设备写入')
          else
            ...mbStructured(scope),
        ], confirm: '明确启动'))
          return false;
        if (host.readOnly && write) throw const FormatException('只读模式禁止开放写入');
        final result = mbMap(
          await host.command({
            'op': 'modbus.controller.start',
            'request': request,
            'listen': value,
            if (scope != null) 'scope': scope,
            'confirmed': true,
          }),
        );
        controllerRun = result['run_id'].toString();
        host.started(result);
        return true;
      },
      confirm: '检查服务范围',
    );
    for (final c in [listen, unit, address, count]) {
      c.dispose();
    }
  }

  Future<void> statistics() async {
    final data = mbMap(await host.command({'op': 'modbus.stats'})),
        session = mbMap(data['session']);
    if (!context().mounted) return;
    await mbShow(context(), '会话统计', [
      mbCard('本次应用会话累计', [
        for (final key in [
          'reads',
          'writes',
          'success',
          'errors',
          'cancelled',
          'duration_ms',
        ])
          mbPair(key, session[key] ?? 0),
      ]),
      mbCard('最近前台操作', [
        for (final key in ['operations', 'success', 'failure', 'duration_ms'])
          mbPair(key, data[key] ?? 0),
        ...mbStructured(data['last']),
      ]),
    ]);
  }

  Future<void> writeLog() async {
    final path = TextEditingController(
      text: model.params['write_log_file']?.toString() ?? '',
    );
    await form(
      '读取持久写入审计',
      [
        mbField('私有审计路径', path),
        const Text('结果未知或审计结果写入失败时，设备可能已发生变化。请核对设备，勿自动重试。'),
      ],
      () async {
        final result = await host.command({
          'op': 'modbus.write-log',
          'path': path.text.trim(),
        });
        if (context().mounted) await show('写入尝试与结果', result);
        return false;
      },
      confirm: '读取审计',
    );
    path.dispose();
  }
}
