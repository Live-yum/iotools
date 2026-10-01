import 'package:flutter/material.dart';
import 'modbus_activity.dart';
import 'modbus_controller.dart';
import 'modbus_host.dart';
import 'modbus_models.dart';
import 'modbus_rules.dart';
import 'modbus_tools.dart';
import 'modbus_widgets.dart';
export 'modbus_host.dart';

/// Genuine Flutter workspace: all inputs, tables, charts and reviews are Dart
/// widgets; the platform bridge only runs shared finite Go commands.
class ModbusWorkspace extends StatefulWidget {
  const ModbusWorkspace({super.key, required this.host});
  final ModbusHost host;
  @override
  State<ModbusWorkspace> createState() => _ModbusWorkspaceState();
}

class _ModbusWorkspaceState extends State<ModbusWorkspace> {
  late final ModbusController model;
  late final ModbusTools tools;
  bool operationBusy = false;
  String? message;
  @override
  void initState() {
    super.initState();
    model = ModbusController(widget.host)..addListener(_changed);
    tools = ModbusTools(widget.host, model, () => context);
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  @override
  void dispose() {
    model.removeListener(_changed);
    model.dispose();
    super.dispose();
  }

  Future<void> perform(Future<void> Function() action) async {
    if (operationBusy) return;
    setState(() {
      operationBusy = true;
      message = null;
    });
    try {
      await action();
    } catch (error) {
      if (mounted) setState(() => message = error.toString());
    } finally {
      if (mounted) setState(() => operationBusy = false);
    }
  }

  Future<void> form(
    String title,
    List<Widget> children,
    Future<bool> Function() submit, {
    String confirm = '临时应用',
  }) => mbDialog<void>(
    context: context,
    builder: (_) => ModbusFormDialog(
      title: title,
      children: children,
      confirm: confirm,
      onSubmit: submit,
    ),
  );
  Widget action(
    String title,
    Future<void> Function() callback, {
    bool write = false,
    IconData? icon,
  }) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 4),
    child: OutlinedButton.icon(
      key: ValueKey(title),
      onPressed: operationBusy || (write && widget.host.readOnly)
          ? null
          : () => perform(callback),
      icon: Icon(icon ?? Icons.chevron_right),
      label: Padding(
        padding: const EdgeInsets.symmetric(vertical: 12),
        child: Text(title),
      ),
    ),
  );
  String get endpointLabel {
    final text = model.draft['endpoint']?.toString() ?? '';
    final uri = Uri.tryParse(text);
    return uri == null
        ? text
        : uri.replace(userInfo: '', query: '', fragment: '').toString();
  }

  @override
  Widget build(BuildContext context) => DefaultTabController(
    length: 4,
    child: Scaffold(
      appBar: AppBar(
        title: const Text('Modbus 工作区'),
        bottom: const TabBar(
          isScrollable: true,
          tabs: [
            Tab(text: '寄存器'),
            Tab(text: '操作'),
            Tab(text: '离线工具'),
            Tab(text: '会话'),
          ],
        ),
      ),
      body: Column(
        children: [
          if (message != null)
            MaterialBanner(
              content: Text(message!),
              actions: [
                TextButton(
                  onPressed: () => setState(() => message = null),
                  child: const Text('关闭'),
                ),
              ],
            ),
          if (widget.host.readOnly)
            const Padding(
              padding: EdgeInsets.all(8),
              child: Text('只读模式 · 写入和配置保存不可用'),
            ),
          Expanded(
            child: TabBarView(
              children: [_registers(), _operations(), _offline(), _session()],
            ),
          ),
        ],
      ),
    ),
  );
  Widget _registers() {
    final frame = model.current;
    return ListView(
      padding: const EdgeInsets.all(12),
      children: [
        mbCard(
          model.draft['name']?.toString() ??
              model.draft['id']?.toString() ??
              '选择 Modbus 请求',
          [
            mbPair('当前草稿端点', endpointLabel),
            Text(
              '${model.dirty ? '● 临时修改，保存后才写入配置' : '本机视图'} · 缓存 ${model.frames.length} 次 · 淘汰 ${model.cache.evicted} 次',
            ),
            Wrap(
              spacing: 8,
              runSpacing: 4,
              children: [
                OutlinedButton(
                  onPressed: () => perform(_layout),
                  child: const Text('列与矩阵'),
                ),
                OutlinedButton(
                  onPressed: () => perform(_navigate),
                  child: const Text('地址导航'),
                ),
                OutlinedButton(
                  onPressed: () =>
                      perform(() => _annotate(model.selectedAddress)),
                  child: const Text('标签 / 固定'),
                ),
                OutlinedButton(
                  onPressed: () => perform(() => _rule(model.selectedAddress)),
                  child: const Text('规则'),
                ),
                OutlinedButton(
                  onPressed: () => perform(_trend),
                  child: const Text('趋势'),
                ),
              ],
            ),
            Wrap(
              spacing: 8,
              children: [
                FilterChip(
                  label: const Text('仅固定'),
                  selected: model.pinnedOnly,
                  onSelected: (v) => setState(() => model.pinnedOnly = v),
                ),
                FilterChip(
                  label: const Text('矩阵'),
                  selected: model.matrix,
                  onSelected: (v) => setState(() => model.matrix = v),
                ),
                FilterChip(
                  label: const Text('冻结'),
                  selected: model.frozen,
                  onSelected: model.freeze,
                ),
              ],
            ),
          ],
        ),
        if (frame == null)
          mbCard('暂无匹配缓存', [const Text('导航、编辑和查看趋势不会自动读取设备。请在操作页预览并明确读取。')])
        else
          mbCard('缓存响应', [
            mbPair(
              '实际端点 / 单元 / 空间',
              '${frame.source.endpoint} · ${frame.source.unit} · ${frame.source.space}',
            ),
            mbPair('run_id / 采样时间', '${frame.source.runId}\n${frame.time}'),
            if (frame.gap) const Text('此响应失败或被截断，值保持缺失') else _table(frame),
          ]),
        action('应用临时设置到请求草稿', () async {
          widget.host.prepare(mbClone(model.draft));
        }),
        action('保存工作区配置', () async {
          if (await mbConfirm(context, '保存本机请求', [
            Text('保存 ${model.draft['id']} 的布局、标签、规则和地址窗口。'),
          ], confirm: '保存'))
            await model.save();
        }, write: true),
        action(
          '导出可见缓存 CSV',
          () => widget.host.exportText('modbus-visible.csv', model.csv()),
        ),
        action('清除本机趋势缓存', () async {
          if (await mbConfirm(context, '清除缓存', [
            const Text('仅清除本机采样视图。新的实际响应仍会进入缓存。'),
          ], confirm: '清除'))
            model.clearCache();
        }),
      ],
    );
  }

  Widget _table(ModbusFrame frame) {
    final rows = model.visibleRows(frame);
    if (rows.isEmpty) return const Text('当前窗口无缓存值；不会跨响应拼补寄存器');
    if (model.matrix) {
      final count =
          (int.tryParse(model.params['matrix_columns'].toString()) ?? 4).clamp(
            1,
            16,
          );
      return SizedBox(
        height: 360,
        child: SingleChildScrollView(
          scrollDirection: Axis.horizontal,
          child: SizedBox(
            width: count * 108.0,
            child: GridView.builder(
              itemCount: rows.length,
              gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
                crossAxisCount: count,
                childAspectRatio: 1.3,
              ),
              itemBuilder: (_, i) => Card(
                child: InkWell(
                  onTap: () => _inspect(frame, rows[i]),
                  child: Center(
                    child: Text(
                      '${model.display(frame, rows[i], 'address')}\n${rows[i]['u16']}',
                    ),
                  ),
                ),
              ),
            ),
          ),
        ),
      );
    }
    final visible = (model.columns['visible'] as List).cast<String>();
    final widths = mbMap(model.columns['widths']);
    double width(String key) =>
        (int.tryParse(widths[key].toString()) ??
                    (['custom', 'label'].contains(key) ? 20 : 12))
                .clamp(1, 120) *
            8.0 +
        16;
    final total = visible.fold<double>(0, (sum, key) => sum + width(key));
    return SizedBox(
      height: 380,
      child: SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        child: SizedBox(
          width: total,
          child: Column(
            children: [
              Row(
                children: visible
                    .map(
                      (key) => SizedBox(
                        width: width(key),
                        height: 48,
                        child: Padding(
                          padding: const EdgeInsets.all(8),
                          child: Text(
                            key,
                            style: const TextStyle(fontWeight: FontWeight.bold),
                          ),
                        ),
                      ),
                    )
                    .toList(),
              ),
              Expanded(
                child: ListView.builder(
                  itemCount: rows.length,
                  itemExtent: 60,
                  itemBuilder: (_, i) => InkWell(
                    onTap: () => _inspect(frame, rows[i]),
                    child: Row(
                      children: visible
                          .map(
                            (key) => SizedBox(
                              width: width(key),
                              child: Padding(
                                padding: const EdgeInsets.all(8),
                                child: Text(
                                  model.display(frame, rows[i], key),
                                  maxLines: 2,
                                  overflow: TextOverflow.ellipsis,
                                ),
                              ),
                            ),
                          )
                          .toList(),
                    ),
                  ),
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }

  Future<void> _inspect(ModbusFrame frame, Map<String, dynamic> row) async {
    final address = boundedInt(row['address'], 0, 65535, '地址');
    model.selectedAddress = address;
    await mbShow(context, '寄存器 ${model.formatAddress(address)}', [
      mbPair('采样时间', frame.time),
      for (final key in modbusColumns)
        if (row.containsKey(key) || key == 'label')
          mbPair(key, model.display(frame, row, key)),
      OutlinedButton(
        onPressed: () => _annotate(address),
        child: const Text('编辑标签与固定'),
      ),
      OutlinedButton(
        onPressed: () => _rule(address),
        child: const Text('编辑规则'),
      ),
      OutlinedButton(onPressed: _trend, child: const Text('查看同地址趋势')),
    ]);
  }

  Future<void> _layout() async {
    final columns = mbClone(model.columns),
        enabled = (columns['visible'] as List).cast<String>().toSet();
    final order = [
      ...(columns['visible'] as List).cast<String>(),
      ...modbusColumns.where((c) => !enabled.contains(c)),
    ];
    final widths = {
      for (final key in order)
        key: TextEditingController(
          text: mbMap(columns['widths'])[key]?.toString() ?? '',
        ),
    };
    final matrix = TextEditingController(
      text: (model.params['matrix_columns'] ?? 4).toString(),
    );
    var radix = columns['address_mode']?.toString() ?? 'decimal',
        time = columns['time_mode']?.toString() ?? 'read_at';
    await form(
      '列布局与矩阵',
      [
        StatefulBuilder(
          builder: (_, refresh) => Column(
            children: [
              mbSelect('地址显示', radix, const [
                'decimal',
                'hex',
              ], (v) => refresh(() => radix = v)),
              mbSelect('时间显示', time, const [
                'read_at',
                'ago',
              ], (v) => refresh(() => time = v)),
              mbField('矩阵列数（1–16）', matrix, numeric: true),
              for (final key in order)
                Row(
                  children: [
                    Expanded(
                      child: CheckboxListTile(
                        contentPadding: EdgeInsets.zero,
                        title: Text(key),
                        value: enabled.contains(key),
                        onChanged: (v) => refresh(() {
                          if (v == true) {
                            enabled.add(key);
                          } else {
                            enabled.remove(key);
                          }
                        }),
                      ),
                    ),
                    SizedBox(
                      width: 74,
                      child: mbField('$key 列宽', widths[key]!, numeric: true),
                    ),
                    IconButton(
                      tooltip: '$key 上移',
                      onPressed: order.indexOf(key) == 0
                          ? null
                          : () => refresh(() {
                              final index = order.indexOf(key);
                              order.removeAt(index);
                              order.insert(index - 1, key);
                            }),
                      icon: const Icon(Icons.arrow_upward),
                    ),
                    IconButton(
                      tooltip: '$key 下移',
                      onPressed: order.indexOf(key) == order.length - 1
                          ? null
                          : () => refresh(() {
                              final index = order.indexOf(key);
                              order.removeAt(index);
                              order.insert(index + 1, key);
                            }),
                      icon: const Icon(Icons.arrow_downward),
                    ),
                  ],
                ),
            ],
          ),
        ),
      ],
      () async {
        final next = {
          'visible': order.where(enabled.contains).toList(),
          'widths': {
            for (final key in order)
              if (widths[key]!.text.trim().isNotEmpty)
                key: boundedInt(widths[key]!.text, 1, 120, '列宽'),
          },
          'address_mode': radix,
          'time_mode': time,
        };
        validateColumns(next);
        final p = mbClone(model.params)..['columns'] = next;
        p['matrix_columns'] = boundedInt(matrix.text, 1, 16, '矩阵列数');
        model.editParams(p);
        return true;
      },
    );
    for (final controller in widths.values) {
      controller.dispose();
    }
    matrix.dispose();
  }

  Future<void> _navigate() async {
    final p = model.params;
    final address = TextEditingController(text: (p['address'] ?? 0).toString()),
        count = TextEditingController(text: (p['count'] ?? 1).toString()),
        unit = TextEditingController(text: (p['unit'] ?? 1).toString());
    var action = readAction(model.draft['action'].toString()),
        order = p['word_order']?.toString() ?? 'ABCD';
    await form(
      '地址与读取窗口',
      [
        mbField('地址 / 0x / 相对值 / 唯一标签', address),
        mbField('数量', count, numeric: true),
        mbField('单元（1–247）', unit, numeric: true),
        StatefulBuilder(
          builder: (_, refresh) => Column(
            children: [
              mbSelect(
                '寄存器空间',
                action,
                modbusReadActions,
                (v) => refresh(() => action = v),
              ),
              mbSelect(
                '字节顺序',
                order,
                modbusWordOrders,
                (v) => refresh(() => order = v),
              ),
            ],
          ),
        ),
        Wrap(
          spacing: 8,
          children: [
            OutlinedButton(
              onPressed: () => address.text = '-${count.text}',
              child: const Text('上一窗口'),
            ),
            OutlinedButton(
              onPressed: () => address.text = '+${count.text}',
              child: const Text('下一窗口'),
            ),
            OutlinedButton(
              onPressed: () => count.text =
                  '${((int.tryParse(count.text) ?? 1) - 1).clamp(1, 2000)}',
              child: const Text('数量 −1'),
            ),
            OutlinedButton(
              onPressed: () => count.text =
                  '${((int.tryParse(count.text) ?? 1) + 1).clamp(1, 2000)}',
              child: const Text('数量 +1'),
            ),
          ],
        ),
        const Text('应用仅改变本机窗口。读取需要另行预览并明确执行。'),
      ],
      () async {
        await model.navigate(
          address: address.text,
          count: boundedInt(count.text, 1, 2000, '数量'),
          unit: boundedInt(unit.text, 1, 247, '单元'),
          action: action,
          order: order,
        );
        return true;
      },
      confirm: '仅应用窗口',
    );
    address.dispose();
    count.dispose();
    unit.dispose();
  }

  Future<void> _annotate(int initial) async {
    final at = TextEditingController(text: '$initial'),
        labels = mbClone(mbMap(model.params['labels']));
    final label = TextEditingController(
      text: labels['$initial']?.toString() ?? '',
    );
    final pins = (model.params['pins'] as List? ?? [])
        .map((v) => boundedInt(v, 0, 65535, '固定地址'))
        .toSet();
    var pinned = pins.contains(initial);
    await form(
      '标签与固定',
      [
        mbField('寄存器地址', at, numeric: true),
        mbField('标签（留空删除）', label),
        StatefulBuilder(
          builder: (_, refresh) => CheckboxListTile(
            title: const Text('固定此地址'),
            value: pinned,
            onChanged: (v) => refresh(() => pinned = v ?? false),
          ),
        ),
      ],
      () async {
        final address = boundedInt(at.text, 0, 65535, '地址');
        if (label.text.length > 1024)
          throw const FormatException('标签最多 1024 字符');
        if (label.text.isEmpty) {
          labels.remove('$address');
        } else {
          labels['$address'] = label.text;
        }
        if (pinned) {
          pins.add(address);
        } else {
          pins.remove(address);
        }
        final p = mbClone(model.params)..['labels'] = labels;
        p['pins'] = pins.toList()..sort();
        model.editParams(p);
        model.selectedAddress = address;
        return true;
      },
    );
    at.dispose();
    label.dispose();
  }

  Future<void> _rule(int address) => mbDialog<void>(
    context: context,
    builder: (_) =>
        ModbusRuleEditor(host: widget.host, model: model, address: address),
  );
  Future<void> _trend() => mbDialog<void>(
    context: context,
    builder: (_) => ModbusTrendDialog(model: model),
  );

  Widget _operations() => ListView(
    padding: const EdgeInsets.all(12),
    children: [
      mbCard('连接与读取', [
        action('连接参数', _connection),
        action(
          '预览并读取当前窗口',
          () => widget.host.reviewAndRun({
            ...mbClone(model.draft),
            'action': readAction(model.draft['action'].toString()),
          }),
        ),
        action('采样参数', _sampling),
        action('暂停读取循环', () async {
          await widget.host.command({'op': 'modbus.pause'});
        }),
        action('恢复读取循环', () async {
          await widget.host.command({'op': 'modbus.resume'});
        }),
        action('停止当前操作', () async {
          final run = widget.host.state['run_id']?.toString() ?? '';
          if (run.isNotEmpty)
            await widget.host.command({'op': 'cancel', 'run_id': run});
        }),
      ]),
      mbCard('明确写入', [
        const Text('每次操作都经过共享引擎预览，核对实际端点、单元、范围与编码后再确认。'),
        action('写入类型数值 / 寄存器', () => _write('write-typed'), write: true),
        action('写入单个线圈', () => _write('write-coil'), write: true),
        action('写入多个寄存器', () => _write('write-registers'), write: true),
        action('写入多个线圈', () => _write('write-coils'), write: true),
        action('FC23 读写事务', () => _write('read-write-registers'), write: true),
        action('持久写入审计设置', _writeAudit),
      ]),
      mbCard('设备与有限探测', [
        action('读取设备标识', _deviceId),
        action('原始 PDU', _raw),
        action('范围扫描 / 搜索', _sweep),
        action('单元探测', _probe),
        action('网络发现', tools.discovery),
        action('网络发现结果 / 选择', tools.discoveryResults),
        action('本机 HTTP 服务', tools.controller),
        ..._selectionResults(),
      ]),
    ],
  );
  Future<void> _connection() async {
    final p = mbClone(model.params),
        endpoint = TextEditingController(
          text: model.draft['endpoint']?.toString() ?? 'mock://local',
        );
    final timeout = TextEditingController(
      text: model.draft['timeout']?.toString() ?? '10s',
    );
    final unit = TextEditingController(text: (p['unit'] ?? 1).toString());
    final connect = TextEditingController(
          text: (p['connect_timeout_ms'] ?? 1000).toString(),
        ),
        request = TextEditingController(
          text: (p['request_timeout_ms'] ?? 1000).toString(),
        ),
        gap = TextEditingController(
          text: (p['request_gap_ms'] ?? 0).toString(),
        );
    var transport = endpoint.text.startsWith('rtu+tcp://')
        ? 'rtu+tcp'
        : endpoint.text.startsWith('usb://')
        ? 'usb'
        : endpoint.text.startsWith('mock://')
        ? 'mock'
        : 'tcp';
    await form(
      '连接参数 · 本机草稿',
      [
        StatefulBuilder(
          builder: (_, refresh) => mbSelect(
            '传输',
            transport,
            const ['tcp', 'rtu+tcp', 'usb', 'mock'],
            (v) {
              refresh(() => transport = v);
              if (v == 'mock') endpoint.text = 'mock://local';
            },
          ),
        ),
        mbField('端点 URI（含主机与端口）', endpoint),
        const Text('USB 端点需先通过应用的 USB 设备选择器选择并授权；这里不会弹出权限或自动连接。'),
        mbField('单元', unit, numeric: true),
        mbField('总超时', timeout),
        mbField('连接超时 ms', connect, numeric: true),
        mbField('请求超时 ms', request, numeric: true),
        mbField('请求间隔 ms', gap, numeric: true),
      ],
      () async {
        final uri = Uri.tryParse(endpoint.text.trim());
        if (uri == null ||
            uri.scheme != transport ||
            (transport == 'mock' && endpoint.text != 'mock://local'))
          throw const FormatException('端点与所选传输不一致');
        if (['tcp', 'rtu+tcp'].contains(transport) &&
            (uri.host.isEmpty || !uri.hasPort))
          throw const FormatException('明确填写主机和端口');
        p.addAll({
          'unit': boundedInt(unit.text, 1, 247, '单元'),
          'connect_timeout_ms': boundedInt(connect.text, 1, 300000, '连接超时'),
          'request_timeout_ms': boundedInt(request.text, 1, 300000, '请求超时'),
          'request_gap_ms': boundedInt(gap.text, 0, 60000, '请求间隔'),
        });
        final next = mbClone(model.draft)
          ..addAll({
            'endpoint': endpoint.text.trim(),
            'timeout': timeout.text.trim(),
            'params': p,
          });
        model.edit(next);
        return true;
      },
    );
    for (final c in [endpoint, timeout, unit, connect, request, gap]) {
      c.dispose();
    }
  }

  Future<void> _sampling() async {
    final samples = TextEditingController(
          text: (model.params['samples'] ?? 1).toString(),
        ),
        interval = TextEditingController(
          text: (model.params['interval_ms'] ?? 1000).toString(),
        );
    await form(
      '有限采样',
      [
        mbField('样本数（1–100000）', samples, numeric: true),
        mbField('间隔 ms（至少 10）', interval, numeric: true),
        const Text('暂停保留原样本预算和截止时间。恢复不会重新排队写入。'),
      ],
      () async {
        final p = mbClone(model.params)
          ..addAll({
            'samples': boundedInt(samples.text, 1, 100000, '样本数'),
            'interval_ms': boundedInt(interval.text, 10, 86400000, '间隔'),
          });
        model.editParams(p);
        return true;
      },
    );
    samples.dispose();
    interval.dispose();
  }

  Future<void> _write(String action) async {
    final p = mbClone(model.params),
        address = TextEditingController(text: '${model.selectedAddress}'),
        value = TextEditingController(text: '0'),
        readAddress = TextEditingController(
          text: (p['address'] ?? 0).toString(),
        ),
        readCount = TextEditingController(text: (p['count'] ?? 1).toString());
    var type = 'u16',
        order = p['word_order']?.toString() ?? 'ABCD',
        coil = false;
    await form(
      '写入草稿 · $action',
      [
        mbPair('目标', '$endpointLabel · 单元 ${p['unit'] ?? 1}'),
        mbField('写入起始地址', address, numeric: true),
        StatefulBuilder(
          builder: (_, refresh) => Column(
            children: [
              if (action == 'write-typed') ...[
                mbSelect(
                  '数值类型',
                  type,
                  modbusValueTypes,
                  (v) => refresh(() => type = v),
                ),
                mbSelect(
                  '写入字节顺序',
                  order,
                  modbusWordOrders,
                  (v) => refresh(() => order = v),
                ),
                mbField('精确数值', value),
                Wrap(
                  spacing: 8,
                  children: [
                    OutlinedButton(
                      onPressed: () {
                        final n = BigInt.tryParse(value.text);
                        if (n != null) value.text = (n - BigInt.one).toString();
                      },
                      child: const Text('−1'),
                    ),
                    OutlinedButton(
                      onPressed: () {
                        final n = BigInt.tryParse(value.text);
                        if (n != null) value.text = (n + BigInt.one).toString();
                      },
                      child: const Text('+1'),
                    ),
                  ],
                ),
                if (type == 'u16')
                  Wrap(
                    children: [
                      for (var bit = 0; bit < 16; bit++)
                        FilterChip(
                          label: Text('位 $bit'),
                          selected:
                              ((BigInt.tryParse(value.text) ?? BigInt.zero) &
                                  (BigInt.one << bit)) !=
                              BigInt.zero,
                          onSelected: (_) => refresh(() {
                            final n =
                                BigInt.tryParse(value.text) ?? BigInt.zero;
                            value.text = (n ^ (BigInt.one << bit)).toString();
                          }),
                        ),
                    ],
                  ),
              ] else if (action == 'write-coil')
                SwitchListTile(
                  title: const Text('线圈 ON'),
                  value: coil,
                  onChanged: (v) => refresh(() => coil = v),
                )
              else
                mbField(
                  action == 'write-coils'
                      ? '线圈值（逗号分隔 0/1）'
                      : '寄存器字（逗号分隔 0–65535）',
                  value,
                  multiline: true,
                ),
            ],
          ),
        ),
        if (action == 'read-write-registers') ...[
          mbField('FC23 读取起始地址', readAddress, numeric: true),
          mbField('FC23 读取数量', readCount, numeric: true),
        ],
        const Text('这里只构造一次操作。下一步必须核对引擎返回的实际目标与编码。'),
      ],
      () async {
        if (widget.host.readOnly) throw const FormatException('只读模式禁止设备写入');
        p['address'] = boundedInt(address.text, 0, 65535, '写入地址');
        p['samples'] = 1;
        if (['write-coil', 'write-coils'].contains(action) &&
            registerSpace(model.draft['action'].toString()) != 'coil') {
          p.remove('pins');
          p.remove('labels');
          p.remove('rules');
        }
        if (action == 'write-typed') {
          p['value_type'] = type;
          p['value'] = exactTypedValue(type, value.text);
          p['word_order'] = order;
        } else if (action == 'write-coil') {
          p['value'] = coil;
        } else {
          final chunks = value.text
              .split(RegExp(r'[,\s]+'))
              .where((v) => v.isNotEmpty)
              .toList();
          final max = action == 'write-coils'
              ? 1968
              : action == 'read-write-registers'
              ? 121
              : 123;
          if (chunks.isEmpty || chunks.length > max)
            throw FormatException('请输入 1–$max 个值');
          p['values'] = action == 'write-coils'
              ? chunks.map((v) => boundedInt(v, 0, 1, '线圈') == 1).toList()
              : chunks.map((v) => boundedInt(v, 0, 65535, '寄存器字')).toList();
        }
        if (action == 'read-write-registers') {
          p['count'] = (p['values'] as List).length;
          p['read_address'] = boundedInt(readAddress.text, 0, 65535, '读取地址');
          p['read_count'] = boundedInt(readCount.text, 1, 125, '读取数量');
        }
        final next = mbClone(model.draft)
          ..addAll({'action': action, 'params': p});
        await widget.host.reviewAndRun(next);
        return true;
      },
      confirm: '预览编码与目标',
    );
    for (final c in [address, value, readAddress, readCount]) {
      c.dispose();
    }
  }

  Future<void> _deviceId() async {
    final code = TextEditingController(text: '1'),
        object = TextEditingController(text: '0');
    await form(
      '读取设备标识',
      [
        mbField('读取代码（1–4）', code, numeric: true),
        mbField('对象 ID（0–255）', object, numeric: true),
      ],
      () async {
        final p = mbClone(model.params)
          ..addAll({
            'read_code': boundedInt(code.text, 1, 4, '读取代码'),
            'object_id': boundedInt(object.text, 0, 255, '对象 ID'),
          });
        await widget.host.reviewAndRun({
          ...mbClone(model.draft),
          'action': 'read-device-id',
          'params': p,
        });
        return true;
      },
      confirm: '预览读取',
    );
    code.dispose();
    object.dispose();
  }

  Future<void> _raw() async {
    final hex = TextEditingController(text: '0300000001'),
        address = TextEditingController(
          text: (model.params['address'] ?? 0).toString(),
        ),
        count = TextEditingController(
          text: (model.params['count'] ?? 1).toString(),
        );
    var action = 'read-raw';
    await form(
      '原始 PDU',
      [
        StatefulBuilder(
          builder: (_, refresh) => mbSelect('操作分类', action, const [
            'read-raw',
            'write-raw',
          ], (v) => refresh(() => action = v)),
        ),
        mbField('PDU 十六进制（首字节为功能码）', hex, multiline: true),
        mbField('明确操作起始地址', address, numeric: true),
        mbField('明确操作数量', count, numeric: true),
        const Text('引擎会解析功能码与读写范围。未知或危险功能码不能绕过写入授权。'),
      ],
      () async {
        if (action == 'write-raw' && widget.host.readOnly)
          throw const FormatException('只读模式禁止写 PDU');
        final p = mbClone(model.params)
          ..addAll({
            'pdu_hex': hex.text.trim(),
            'address': boundedInt(address.text, 0, 65535, '地址'),
            'count': boundedInt(count.text, 1, 2000, '数量'),
          });
        await widget.host.reviewAndRun({
          ...mbClone(model.draft),
          'action': action,
          'params': p,
        });
        return true;
      },
      confirm: '解析并预览',
    );
    hex.dispose();
    address.dispose();
    count.dispose();
  }

  Future<void> _sweep() async {
    var action = 'sweep-holding', recover = false;
    final start = TextEditingController(
          text: (model.params['address'] ?? 0).toString(),
        ),
        end = TextEditingController(text: '15'),
        batch = TextEditingController(text: '16'),
        cycles = TextEditingController(text: '1'),
        match = TextEditingController(text: '0');
    await form(
      '有限范围扫描 / 搜索',
      [
        StatefulBuilder(
          builder: (_, refresh) => Column(
            children: [
              mbSelect('扫描空间 / 搜索', action, const [
                'sweep-holding',
                'sweep-input',
                'sweep-coils',
                'sweep-discrete',
                'search-holding',
              ], (v) => refresh(() => action = v)),
              SwitchListTile(
                title: const Text('失败后按单地址恢复'),
                value: recover,
                onChanged: (v) => refresh(() => recover = v),
              ),
            ],
          ),
        ),
        mbField('起始地址', start, numeric: true),
        mbField('结束地址（含）', end, numeric: true),
        mbField('每批数量', batch, numeric: true),
        mbField('扫描周期', cycles, numeric: true),
        mbField('搜索匹配原始值', match, numeric: true),
        const Text('最多 16000 个地址、1000 周期、100000 次事务。失败批次保持未知，不会生成假值。'),
      ],
      () async {
        final a = boundedInt(start.text, 0, 65535, '起始地址'),
            z = boundedInt(end.text, a, 65535, '结束地址'),
            n = boundedInt(batch.text, 1, 125, '每批数量'),
            c = boundedInt(cycles.text, 1, 1000, '周期');
        if (z - a + 1 > 16000 ||
            ((z - a + n) ~/ n) * c > 100000 ||
            (recover && (z - a + 1) * c > 100000))
          throw const FormatException('超出有限扫描预算');
        final p = mbClone(model.params)
          ..addAll({
            'address': a,
            'end_address': z,
            'count': n,
            'sweep_cycles': c,
            'sweep_recover': recover,
          });
        if (action == 'search-holding')
          p['match_value'] = boundedInt(match.text, 0, 65535, '匹配值');
        await widget.host.reviewAndRun({
          ...mbClone(model.draft),
          'action': action,
          'params': p,
        });
        return true;
      },
      confirm: '预览完整范围',
    );
    for (final c in [start, end, batch, cycles, match]) {
      c.dispose();
    }
  }

  Future<void> _probe() async {
    final units = TextEditingController(
          text: (model.params['unit'] ?? 1).toString(),
        ),
        address = TextEditingController(
          text: (model.params['address'] ?? 0).toString(),
        ),
        count = TextEditingController(text: '1');
    var space = 'read-holding', first = true;
    await form(
      '明确单元探测',
      [
        mbField('单元 ID 列表（逗号分隔）', units),
        mbField('探测地址', address, numeric: true),
        mbField('探测数量', count, numeric: true),
        StatefulBuilder(
          builder: (_, refresh) => Column(
            children: [
              mbSelect(
                '探测空间',
                space,
                modbusReadActions,
                (v) => refresh(() => space = v),
              ),
              SwitchListTile(
                title: const Text('首个成功后停止'),
                value: first,
                onChanged: (v) => refresh(() => first = v),
              ),
            ],
          ),
        ),
      ],
      () async {
        final ids = units.text
            .split(RegExp(r'[,\s]+'))
            .where((v) => v.isNotEmpty)
            .map((v) => boundedInt(v, 1, 247, '单元'))
            .toList();
        if (ids.isEmpty || ids.length > 32 || ids.toSet().length != ids.length)
          throw const FormatException('请明确列出 1–32 个不同单元 ID');
        final p = mbClone(model.params)
          ..addAll({
            'units': ids,
            'address': boundedInt(address.text, 0, 65535, '地址'),
            'count': boundedInt(count.text, 1, 125, '数量'),
            'scan_type': space,
            'stop_first': first,
          });
        await widget.host.reviewAndRun({
          ...mbClone(model.draft),
          'action': 'scan-units',
          'params': p,
        });
        return true;
      },
      confirm: '预览探测范围',
    );
    units.dispose();
    address.dispose();
    count.dispose();
  }

  List<Widget> _selectionResults() {
    final source = widget.host.resultRequest;
    if (source == null) return [];
    return [
      for (final event
          in widget.host.events
              .where(
                (e) =>
                    e['kind'] == 'unit-probe' &&
                    e['run_id'] == widget.host.resultRunId,
              )
              .toList()
              .reversed
              .take(32))
        mbCard('单元 ${mbMap(event['data'])['unit']}', [
          mbPair('实际探测端点', source['endpoint']),
          mbPair(
            '状态',
            mbMap(event['data'])['responsive'] == true
                ? '有响应'
                : mbMap(event['data'])['exception'] == true
                ? '协议异常响应'
                : mbMap(event['data'])['error'],
          ),
          OutlinedButton(
            onPressed: () {
              final p = mbClone(model.params)
                ..['unit'] = mbMap(event['data'])['unit'];
              final next = mbClone(model.draft)
                ..addAll({'params': p, 'endpoint': source['endpoint']});
              model.edit(next);
              widget.host.prepare(next);
            },
            child: const Text('仅选择此连接与单元到草稿'),
          ),
        ]),
    ];
  }

  Future<void> _writeAudit() async {
    final path = TextEditingController(
      text: model.params['write_log_file']?.toString() ?? '',
    );
    await form(
      '写入审计',
      [
        mbField('应用私有审计文件（留空禁用）', path),
        const Text('审计打开失败会阻止写入；结果记录失败时设备可能已写入，不会自动重试。'),
      ],
      () async {
        final p = mbClone(model.params);
        if (path.text.trim().isEmpty) {
          p.remove('write_log_file');
        } else {
          p['write_log_file'] = path.text.trim();
        }
        model.editParams(p);
        return true;
      },
    );
    path.dispose();
  }

  Widget _offline() => ListView(
    padding: const EdgeInsets.all(12),
    children: [
      mbCard('寄存器文件与转换', [
        action('导入当前空间注释', tools.importAnnotations),
        action('导出当前空间注释', tools.exportAnnotations),
        action('导入完整 MTUI 配置', tools.importMTUI),
        action('保存当前缓存快照', tools.saveSnapshot),
        action('加载 / 比较快照', tools.compareSnapshots),
        action('CSV 离线差异', tools.csvDiff),
      ]),
      mbCard('工作方式', [
        const Text(
          '布局、导航、规则和离线比较只使用本机数据。配置保存是单独动作。\n跨空间不会合并标签或规则，跨端点与单元不会混用快照。',
        ),
        const Text('文件导入、集合轮转和 USB 授权入口位于应用的文件与设备页面。'),
      ]),
    ],
  );
  Widget _session() => ListView(
    padding: const EdgeInsets.all(12),
    children: [
      action('刷新会话统计', tools.statistics),
      action('查看持久写入审计', tools.writeLog),
      ModbusActivityView(host: widget.host),
      for (final event
          in widget.host.events
              .where(
                (e) => [
                  'sweep-progress',
                  'sweep-error',
                  'register-match',
                  'device-identification',
                  'raw-pdu',
                  'done',
                ].contains(e['kind']),
              )
              .toList()
              .reversed
              .take(200))
        mbCard(event['kind'].toString(), [
          mbPair('时间 / run_id', '${event['time']}\n${event['run_id']}'),
          ..._eventDetails(event),
        ]),
    ],
  );
  List<Widget> _eventDetails(Map<String, dynamic> event) {
    final data = mbMap(event['data']);
    if (event['kind'] == 'done')
      return [
        mbPair('操作状态', data['status']),
        if (data['error'] != null) mbPair('错误详情', data['error']),
      ];
    if (event['kind'] == 'device-identification')
      return [
        mbPair('设备单元 / 一致性', '${data['unit']} / ${data['conformity']}'),
        for (final object in mbMap(data['objects']).entries)
          mbPair('对象 ID ${object.key}', object.value),
      ];
    if (event['kind'] == 'raw-pdu')
      return [
        mbPair('请求 PDU（hex）', data['request_hex']),
        mbPair('响应 PDU（hex）', data['response_hex']),
      ];
    return mbStructured(data);
  }
}

class ModbusTrendDialog extends StatefulWidget {
  const ModbusTrendDialog({super.key, required this.model});
  final ModbusController model;
  @override
  State<ModbusTrendDialog> createState() => _ModbusTrendDialogState();
}

class _ModbusTrendDialogState extends State<ModbusTrendDialog> {
  late final TextEditingController address;
  String field = 'u16';
  bool frozen = false;
  List<ModbusFrame> fixed = [];
  @override
  void initState() {
    super.initState();
    address = TextEditingController(text: '${widget.model.selectedAddress}');
    field = widget.model.selectedField;
    widget.model.addListener(changed);
  }

  void changed() {
    if (mounted && !frozen) setState(() {});
  }

  @override
  void dispose() {
    widget.model.removeListener(changed);
    address.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final at = int.tryParse(address.text) ?? 0;
    final trend = ModbusTrend(frozen ? fixed : widget.model.frames, at, field);
    return AlertDialog(
      title: const Text('同次响应历史与趋势'),
      content: SizedBox(
        width: 620,
        child: SingleChildScrollView(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            mainAxisSize: MainAxisSize.min,
            children: [
              mbField('观察地址', address),
              mbSelect('趋势字段', field, const [
                'u16',
                'i16',
                'u32',
                'i32',
                'u64',
                'i64',
                'f16',
                'f32',
                'f64',
                'bcd',
                'bcd32',
                'custom_numeric',
              ], (v) => setState(() => field = v)),
              OutlinedButton(
                onPressed: () {
                  if (at >= 0 && at <= 65535)
                    setState(() {
                      widget.model.selectedAddress = at;
                      widget.model.selectedField = field;
                    });
                },
                child: const Text('更新图表'),
              ),
              SwitchListTile(
                title: const Text('冻结所选历史'),
                value: frozen,
                onChanged: (v) => setState(() {
                  if (v) fixed = List.of(widget.model.frames);
                  frozen = v;
                }),
              ),
              mbPair('NOW（精确）', trend.now),
              mbPair(
                'MIN / MAX（精确）',
                '${trend.minimum ?? '—'} / ${trend.maximum ?? '—'}',
              ),
              mbPair('AVG（近似，截断至 16 位小数）', trend.average),
              mbPair('有效样本', '${trend.valid} / ${trend.points.length}'),
              const Text('图形为近似位置，64 位值请以上方精确文本为准。缺失和非有限值形成断点，不跨响应拼接。'),
              SizedBox(
                height: 200,
                child: CustomPaint(painter: _TrendPainter(trend)),
              ),
            ],
          ),
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('关闭'),
        ),
      ],
    );
  }
}

class _TrendPainter extends CustomPainter {
  _TrendPainter(this.trend);
  final ModbusTrend trend;
  @override
  void paint(Canvas canvas, Size size) {
    final paint = Paint()
      ..color = const Color(0xff4fd8f5)
      ..strokeWidth = 2;
    if (trend.minimum == null || trend.maximum == null) return;
    final range = (trend.maximum! - trend.minimum!).toDouble();
    Offset? previous;
    for (var i = 0; i < trend.points.length; i++) {
      final value = trend.points[i];
      if (value == null) {
        previous = null;
        continue;
      }
      final relative = range == 0
          ? 0.5
          : (value - trend.minimum!).toDouble() / range;
      if (!relative.isFinite) {
        previous = null;
        continue;
      }
      final point = Offset(
        12 +
            (size.width - 24) *
                i /
                (trend.points.length <= 1 ? 1 : trend.points.length - 1),
        12 + (size.height - 24) * (1 - relative),
      );
      if (previous != null) canvas.drawLine(previous, point, paint);
      canvas.drawCircle(point, 3, paint);
      previous = point;
    }
  }

  @override
  bool shouldRepaint(covariant _TrendPainter oldDelegate) => true;
}
