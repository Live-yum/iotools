import 'dart:async';
import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'opcua_controller.dart';
import 'opcua_models.dart';
import 'opcua_typed_editor.dart';
export 'opcua_models.dart';
part 'opcua_workspace_dialogs.dart';

/// Touch-first OPC UA route. All network operations require an explicit action.
class OpcuaWorkspace extends StatefulWidget {
  const OpcuaWorkspace({
    super.key,
    required this.request,
    required this.command,
    required this.events,
    required this.readOnly,
    required this.onPrepare,
    this.onStarted,
    this.exportText,
  });
  final UaMap request;
  final UaCommand command;
  final Stream<UaMap> events;
  final bool readOnly;
  final ValueChanged<UaMap> onPrepare;
  final ValueChanged<UaMap>? onStarted;
  final Future<void> Function(String name, String text)? exportText;
  @override
  State<OpcuaWorkspace> createState() => _OpcuaWorkspaceState();
}

class _OpcuaWorkspaceState extends State<OpcuaWorkspace>
    with WidgetsBindingObserver {
  late UaController model;
  String section = 'browse';
  int page = 0;
  final Set<Route<dynamic>> _dialogs = {};
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    model = UaController(
      command: widget.command,
      events: widget.events,
      request: widget.request,
      readOnly: widget.readOnly,
      onStarted: widget.onStarted,
    )..addListener(_changed);
  }

  void _selectSection(String value) {
    setState(() => section = value);
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  @override
  void didUpdateWidget(covariant OpcuaWorkspace oldWidget) {
    super.didUpdateWidget(oldWidget);
    model.readOnly = widget.readOnly;
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.hidden ||
        state == AppLifecycleState.paused ||
        state == AppLifecycleState.detached) {
      if (model.paused) return;
      // Modal values are memory-only; stale confirmations cannot survive a pause.
      unawaited(model.background());
      for (final route in List<Route<dynamic>>.from(_dialogs).reversed) {
        if (route.isActive) route.navigator?.removeRoute(route);
      }
    } else if (state == AppLifecycleState.resumed) {
      if (!model.paused) return;
      model.resume();
    }
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    model.removeListener(_changed);
    model.dispose();
    super.dispose();
  }

  Future<void> guard(Future<void> Function() action) async {
    try {
      await action();
    } catch (e) {
      model.fail(e);
      if (mounted)
        await _details(
          '请检查输入',
          e is FormatException ? e.message : e.toString(),
        );
    }
  }

  Widget _button(
    String label,
    VoidCallback? action, {
    String? key,
    bool filled = false,
  }) => Padding(
    padding: const EdgeInsets.only(right: 8, bottom: 8),
    child: filled
        ? FilledButton(
            key: key == null ? null : ValueKey(key),
            onPressed: action,
            child: Text(label),
          )
        : OutlinedButton(
            key: key == null ? null : ValueKey(key),
            onPressed: action,
            child: Text(label),
          ),
  );
  Widget _card(String title, List<Widget> children, {Key? key}) => Card(
    key: key,
    margin: const EdgeInsets.only(bottom: 12),
    child: Padding(
      padding: const EdgeInsets.all(16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          Text(title, style: Theme.of(context).textTheme.titleMedium),
          const SizedBox(height: 12),
          ...children,
        ],
      ),
    ),
  );
  Widget _pair(String label, dynamic value, {String? key}) => Padding(
    padding: const EdgeInsets.only(bottom: 8),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Text(
          label,
          style: Theme.of(context).textTheme.labelMedium?.copyWith(
            color: Theme.of(context).colorScheme.secondary,
          ),
        ),
        SelectableText(
          uaText(value).length > 16000
              ? '${uaText(value).substring(0, 16000)}\n显示前 16000 字符；完整详情保留全部数据'
              : uaText(value),
          key: key == null ? null : ValueKey(key),
        ),
      ],
    ),
  );
  Widget _field(
    String label,
    String value,
    ValueChanged<String> update, {
    String? key,
    bool secret = false,
    bool multi = false,
  }) => Padding(
    padding: const EdgeInsets.only(bottom: 14),
    child: TextFormField(
      key: ValueKey(key ?? 'ua-field-$label'),
      initialValue: value,
      obscureText: secret,
      autocorrect: false,
      enableSuggestions: false,
      restorationId: null,
      maxLines: multi ? 3 : 1,
      decoration: InputDecoration(labelText: label),
      onChanged: update,
    ),
  );
  Widget _choice(
    String label,
    List<String> options,
    String value,
    ValueChanged<String> update, {
    String? key,
  }) => Padding(
    padding: const EdgeInsets.only(bottom: 14),
    child: DropdownButtonFormField<String>(
      key: ValueKey(key ?? 'ua-choice-$label'),
      initialValue: options.contains(value) ? value : options.first,
      isExpanded: true,
      decoration: InputDecoration(labelText: label),
      items: options
          .map(
            (v) => DropdownMenuItem(
              value: v,
              child: Text(v, overflow: TextOverflow.ellipsis),
            ),
          )
          .toList(),
      onChanged: (v) {
        if (v != null) update(v);
      },
    ),
  );
  Widget _check(String label, bool value, ValueChanged<bool> update) =>
      SwitchListTile(
        contentPadding: EdgeInsets.zero,
        title: Text(label),
        value: value,
        onChanged: update,
      );
  @override
  Widget build(BuildContext context) {
    final node = model.current;
    return Scaffold(
      appBar: AppBar(
        title: const Text('OPC UA 工作台'),
        actions: [
          IconButton(
            tooltip: '连接、安全与身份',
            icon: const Icon(Icons.shield_outlined),
            onPressed: () => _connection(node.request),
          ),
          IconButton(
            tooltip: '活动记录',
            icon: const Icon(Icons.receipt_long_outlined),
            onPressed: _logs,
          ),
        ],
      ),
      body: SafeArea(
        child: LayoutBuilder(
          builder: (context, screen) {
            final header = _header(node);
            final body = _body(node);
            if (screen.maxHeight < 520 ||
                MediaQuery.textScalerOf(context).scale(1) > 1.3) {
              return SingleChildScrollView(
                primary: false,
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    ...header,
                    SizedBox(
                      height: screen.maxHeight.clamp(260.0, 700.0).toDouble(),
                      child: body,
                    ),
                  ],
                ),
              );
            }
            return Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                ...header,
                Expanded(child: body),
              ],
            );
          },
        ),
      ),
    );
  }

  List<Widget> _header(UaNode node) => [
    Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(
            uaSafeEndpoint(
              node.resolvedEndpoint.isEmpty
                  ? node.request['endpoint']
                  : node.resolvedEndpoint,
            ),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
          SelectableText(
            node.displayId,
            key: const ValueKey('ua-selected-node'),
            style: Theme.of(context).textTheme.titleMedium,
          ),
          if (widget.readOnly)
            Text(
              '只读保护 · 写入与方法调用会被阻止',
              style: TextStyle(color: Theme.of(context).colorScheme.error),
            ),
          Wrap(
            children: [
              _button(
                '后退',
                model.cache.position > 0
                    ? () {
                        model.back();
                        page = 0;
                      }
                    : null,
                key: 'ua-back',
              ),
              _button(
                '前进',
                model.cache.position + 1 < model.cache.history.length
                    ? () {
                        model.forward();
                        page = 0;
                      }
                    : null,
                key: 'ua-forward',
              ),
              _button('跳转', _jump, key: 'ua-jump'),
              _button('复制 ID', () => _copy(node.displayId), key: 'ua-copy'),
            ],
          ),
        ],
      ),
    ),
    SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.symmetric(horizontal: 16),
      child: Row(
        children: [
          for (final entry in const {
            'browse': '浏览',
            'attributes': '属性',
            'references': '引用',
            'subscriptions': '订阅',
            'discovery': '端点',
          }.entries)
            Padding(
              padding: const EdgeInsets.only(right: 8),
              child: ChoiceChip(
                key: ValueKey('ua-tab-${entry.key}'),
                label: Text(entry.value),
                selected: section == entry.key,
                onSelected: (_) {
                  setState(() {
                    section = entry.key;
                    page = 0;
                  });
                  if (section == 'subscriptions')
                    unawaited(model.refreshSubscriptions());
                },
              ),
            ),
        ],
      ),
    ),
    Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
      child: Text(
        model.notice,
        key: const ValueKey('ua-status'),
        maxLines: 3,
        overflow: TextOverflow.ellipsis,
        style: Theme.of(context).textTheme.bodySmall,
      ),
    ),
    if (model.pending) const LinearProgressIndicator(),
    Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
      child: Wrap(
        children: [
          if (section != 'subscriptions')
            _button(
              section == 'discovery'
                  ? '发现端点'
                  : section == 'attributes'
                  ? '刷新全部属性'
                  : section == 'references'
                  ? '刷新引用'
                  : '刷新子节点',
              !model.busy ? () => guard(() => _refresh(node)) : null,
              key: 'ua-refresh',
              filled: true,
            ),
          if (model.foreground.isNotEmpty)
            _button('取消当前任务', () => guard(model.cancel), key: 'ua-cancel-run'),
          if (section == 'browse')
            _button('路径与操作', () => _actions(node), key: 'ua-actions'),
          if (section == 'attributes')
            _button('类型化写入', () => _write(node, 'Value'), key: 'ua-write'),
          if (section == 'references')
            _button(
              '引用筛选',
              () => _referenceFilter(node),
              key: 'ua-reference-filter',
            ),
          if (section == 'discovery') ...[
            _button('连接历史', _connections, key: 'ua-connections'),
            _button('生成客户端身份', _identity, key: 'ua-identity'),
          ],
        ],
      ),
    ),
  ];
  Widget _body(UaNode node) => LayoutBuilder(
    builder: (context, constraints) {
      final main = CustomScrollView(
        key: ValueKey('ua-scroll-$section'),
        slivers: [
          SliverPadding(
            padding: const EdgeInsets.fromLTRB(16, 16, 16, 0),
            sliver: section == 'attributes'
                ? _attributes(node)
                : SliverToBoxAdapter(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: _content(node),
                    ),
                  ),
          ),
          SliverPadding(
            padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
            sliver: SliverToBoxAdapter(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  for (final result in node.largeResults)
                    _card('大型结果 · ${result['kind']}', [
                      _pair('原始字节数', result['original_bytes']),
                      if (result['result_unavailable_reason'] != null)
                        _pair('无法加载', result['result_unavailable_reason']),
                      if (result['result_id'] != null)
                        _button(
                          '明确加载完整结果',
                          () => guard(() async {
                            final full = await widget.command({
                              'op': 'result.get',
                              'result_id': result['result_id'],
                            });
                            if (mounted)
                              await _details(
                                '完整 OPC UA 结果',
                                uaMap(full)['data'],
                              );
                          }),
                        ),
                    ]),
                  if (node.truncated)
                    const Text('结果超出缓存边界或已截断。请缩小范围；未显示的数据不代表不存在。'),
                ],
              ),
            ),
          ),
        ],
      );
      if (constraints.maxWidth < 840 ||
          ['subscriptions', 'discovery'].contains(section))
        return main;
      return Row(
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          SizedBox(
            width: constraints.maxWidth * .36,
            child: SingleChildScrollView(
              primary: false,
              padding: const EdgeInsets.all(16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  Text('缓存节点', style: Theme.of(context).textTheme.titleMedium),
                  ..._referenceCards(node, false),
                ],
              ),
            ),
          ),
          const VerticalDivider(width: 1),
          Expanded(child: main),
        ],
      );
    },
  );

  List<Widget> _content(UaNode node) {
    switch (section) {
      case 'subscriptions':
        return _subscriptions(node);
      case 'discovery':
        return _endpoints(node);
      case 'references':
        return _referenceCards(node, true);
      default:
        return _referenceCards(node, false);
    }
  }

  Future<void> _refresh(UaNode node) async {
    final action = section == 'discovery'
        ? 'discover'
        : section == 'attributes'
        ? 'attributes'
        : section == 'references'
        ? 'references'
        : 'browse';
    final request = uaOperation(node.request, node.id, action);
    if (action == 'references')
      request['params'] = {...uaMap(request['params']), ...node.filter};
    await _execute(request, node: node);
  }

  Future<bool> _execute(
    UaMap request, {
    UaNode? node,
    bool forceReview = false,
  }) async {
    final preview = await model.preview(request, node: node);
    if (preview == null || !mounted) return false;
    if (preview.review || forceReview) {
      final r = uaMap(preview.data['request']), p = uaMap(r['params']);
      final ok = await _confirm(
        preview.data['mutates'] == true
            ? '确认 OPC UA 写入 / 调用'
            : request['action'] == 'subscribe'
            ? '确认独立订阅'
            : '确认读取目标',
        [
          if (preview.changedTarget) const Text('解析后的端点或节点已改变。确认后会清除此位置旧缓存'),
          _pair('端点', uaSafeEndpoint(r['endpoint'])),
          _pair('操作', request['action']),
          _pair('时限', r['timeout']),
          for (final field in [
            'node_id',
            'attribute',
            'value_type',
            'value',
            'object_id',
            'method_id',
            'arguments',
            'interval_ms',
            'max_events',
          ])
            if (p.containsKey(field)) ...[
              _pair(field, p[field]),
              if (uaText(p[field]).length > 16000)
                _button(
                  '完整确切参数 · $field',
                  () => _details('确切执行参数 · $field', p[field]),
                ),
            ],
          if (preview.data['warnings'] != null)
            _pair('警告', preview.data['warnings']),
          const Text('确认只发送这次确切操作。取消不会写入；失败或后台中断不会自动重试。'),
        ],
        key: 'ua-confirm-run',
      );
      if (!ok) {
        model.discard(preview);
        return false;
      }
    }
    return model.start(preview);
  }

  List<Widget> _referenceCards(UaNode node, bool references) {
    final rows = references ? node.references : node.browse;
    final result = <Widget>[];
    if (node.resolved != null) {
      result.add(
        _card('路径解析结果', [
          _pair('路径', node.resolved!['path']),
          _pair('节点', node.resolved!['node_id']),
          _button(
            '打开已解析节点 · 缓存',
            () => model.visit(node.request, '${node.resolved!['node_id']}'),
          ),
        ]),
      );
    }
    if (node.path.isNotEmpty)
      result.add(
        _card('唯一浏览路径', [
          _pair('路径', node.path),
          _button('复制路径', () => _copy(node.path)),
        ]),
      );
    if (rows.isEmpty) result.add(const Text('没有缓存结果。点按刷新才读取；空列表不能证明节点不存在。'));
    final start = page * 25;
    for (var i = start; i < rows.length && i < start + 25; i++) {
      final row = rows[i], id = '${row['node_id'] ?? ''}';
      result.add(
        _card(
          '${row['display_name'] ?? row['browse_name'] ?? id}',
          [
            _pair('节点', id),
            _pair('类别', row['node_class']),
            if (references)
              _pair('方向', row['is_forward'] == true ? '正向' : '反向'),
            _button(
              '选择节点 · 不联网',
              () => guard(() async {
                model.visit(node.request, id);
                page = 0;
              }),
              key: 'ua-node-$id',
            ),
            if ('${row['node_class']}'.toLowerCase().contains('method'))
              _button('方法签名与调用', () => _method(node, id), key: 'ua-method-$id'),
          ],
          key: ValueKey('ua-reference-$id'),
        ),
      );
    }
    if (rows.length > 25)
      result.add(
        Row(
          children: [
            _button('上一页', page > 0 ? () => setState(() => page--) : null),
            Text('${page + 1} / ${(rows.length / 25).ceil()}'),
            _button(
              '下一页',
              (page + 1) * 25 < rows.length
                  ? () => setState(() => page++)
                  : null,
            ),
          ],
        ),
      );
    return result;
  }

  Widget _attributes(UaNode node) {
    if (node.attributes.isEmpty) {
      return const SliverToBoxAdapter(
        child: Text('刷新会读取全部 27 个标准属性，每一项保留自己的状态与时间。'),
      );
    }
    final entries = node.attributes.entries.toList(growable: false);
    // Each card owns six SelectableText clients. Build only the viewport and
    // its cache, while the controller retains every attribute and typed draft.
    return SliverList.builder(
      itemCount: entries.length,
      itemBuilder: (context, index) => _attribute(node, entries[index]),
    );
  }

  Widget _attribute(UaNode node, MapEntry<String, UaMap> entry) => _card(
    entry.key,
    [
      _pair('属性 ID', entry.value['attribute_id']),
      _pair('状态', entry.value['status']),
      _pair('类型', entry.value['value_type_name']),
      _pair('值', entry.value['value'], key: 'ua-attribute-${entry.key}-value'),
      _pair('源时间', entry.value['source_timestamp']),
      _pair('服务器时间', entry.value['server_timestamp']),
      Wrap(
        children: [
          _button(
            '读取此属性',
            () => guard(() async {
              final r = uaOperation(node.request, node.id, 'read');
              r['params'] = {...uaMap(r['params']), 'attribute': entry.key};
              await _execute(r, node: node);
            }),
          ),
          if (uaWritable.contains(entry.key))
            _button('编辑值', () => _write(node, entry.key)),
        ],
      ),
      _button('完整属性详情', () => _details(entry.key, entry.value)),
    ],
    key: ValueKey('ua-attribute-${entry.key}'),
  );

  List<Widget> _subscriptions(UaNode node) => [
    const Text('最多 16 个独立订阅。切换节点和关闭页面保留订阅，进入后台全部停止，返回不自动重连。'),
    const SizedBox(height: 12),
    Wrap(
      children: [
        _button(
          '添加当前节点订阅',
          () => _subscribe(node),
          key: 'ua-add-subscription',
          filled: true,
        ),
        _button(
          '刷新本机状态',
          () => guard(model.refreshSubscriptions),
          key: 'ua-refresh-subscriptions',
        ),
        _button('停止全部订阅', () => guard(model.stopAll), key: 'ua-stop-all'),
      ],
    ),
    if (model.subscriptions.isEmpty) const Text('本机没有订阅记录'),
    for (final sub in model.subscriptions)
      _card('${sub['status']}', [
        _pair('端点', uaSafeEndpoint(sub['endpoint'])),
        _pair('节点', sub['node_ids']),
        _pair('开始时间', sub['started']),
        if (sub['last_event'] != null) ...[
          _pair('最新值', uaMap(sub['last_event'])['value']),
          _pair('状态', uaMap(sub['last_event'])['status']),
          _pair('源时间', uaMap(sub['last_event'])['source_timestamp']),
        ],
        if (sub['last_event'] != null)
          _button('完整通知详情', () => _details('订阅最新通知', sub['last_event'])),
        if (sub['error'] != null) _pair('错误', sub['error']),
        if (sub['status'] == 'running')
          _button(
            '停止此订阅',
            () => guard(() => model.stopSubscription('${sub['id']}')),
            key: 'ua-stop-${sub['id']}',
          ),
      ], key: ValueKey('ua-subscription-${sub['id']}')),
  ];
  List<Widget> _endpoints(UaNode node) => [
    if (model.identity != null)
      _card('新身份已生成', [
        _pair('证书私有路径', model.identity!['cert_path']),
        _pair('私钥私有路径', model.identity!['key_path']),
        _pair('应用 URI', model.identity!['application_uri']),
        if (model.identity!['sha256_fingerprint'] != null)
          _pair('证书 SHA-256', model.identity!['sha256_fingerprint']),
      ]),
    const Text('发现不等于信任。选择只生成连接草稿，证书指纹必须通过独立可信渠道核对。'),
    const SizedBox(height: 12),
    if (node.endpoints.isEmpty) const Text('点按发现端点，读取服务端公布的策略与身份方式。'),
    for (final endpoint in node.endpoints.skip(page * 25).take(25))
      _card(uaSafeEndpoint(endpoint['url']), [
        _pair('安全策略', endpoint['security_policy']),
        _pair('消息模式', uaSecurityMode('${endpoint['security_mode']}')),
        _pair('身份类型', endpoint['identity_tokens']),
        _pair('未验证的指纹', endpoint['certificate_sha256']),
        _button(
          '选择端点并配置草稿',
          () => _connection({
            'id': 'opcua-endpoint-${DateTime.now().millisecondsSinceEpoch}',
            'name': '已选择端点 · 待核对身份',
            'protocol': 'opcua',
            'action': 'browse',
            'endpoint': endpoint['url'],
            'timeout': '10s',
            'params': {
              'node_id': 'i=85',
              'security_policy': '${endpoint['security_policy']}'
                  .split('#')
                  .last,
              'security_mode': endpoint['security_mode'],
              'auth': 'anonymous',
            },
          }, discovered: endpoint),
          key: 'ua-select-endpoint',
        ),
      ]),
    if (node.endpoints.length > 25)
      Row(
        children: [
          _button('上一页端点', page > 0 ? () => setState(() => page--) : null),
          _button(
            '下一页端点',
            (page + 1) * 25 < node.endpoints.length
                ? () => setState(() => page++)
                : null,
          ),
        ],
      ),
  ];
  Future<void> _copy(String text) async {
    await Clipboard.setData(ClipboardData(text: text));
    if (mounted)
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(const SnackBar(content: Text('已复制到剪贴板')));
  }

  Future<void> _details(String title, dynamic value) {
    final text = value is String
        ? value
        : const JsonEncoder.withIndent('  ').convert(value);
    return _dialog(title, [
      SelectableText(
        text.length > 100000
            ? '${text.substring(0, 100000)}\n显示前 100000 字符；复制或导出保留完整结果'
            : text,
      ),
      _button('复制完整内容', () => _copy(text)),
      if (widget.exportText != null)
        _button(
          '明确导出完整结果',
          () => guard(() => widget.exportText!('opcua-result.txt', text)),
        ),
    ]);
  }

  Future<void> _dialog(String title, List<Widget> body) =>
      _dynamicDialog(title, (_) => body);
  Future<void> _dynamicDialog(
    String title,
    List<Widget> Function(StateSetter) body,
  ) async {
    Route<dynamic>? route;
    await showDialog<void>(
      context: context,
      builder: (ctx) {
        route = ModalRoute.of(ctx);
        if (route != null) _dialogs.add(route!);
        return StatefulBuilder(
          builder: (ctx, set) => AnimatedBuilder(
            animation: model,
            builder: (ctx, _) => AlertDialog(
              title: Text(title),
              content: SingleChildScrollView(
                child: SizedBox(
                  width: 560,
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: body(set),
                  ),
                ),
              ),
              actions: [
                TextButton(
                  onPressed: () => Navigator.pop(ctx),
                  child: const Text('关闭'),
                ),
              ],
            ),
          ),
        );
      },
    );
    if (route != null) _dialogs.remove(route);
  }

  Future<bool> _confirm(
    String title,
    List<Widget> body, {
    String key = 'ua-confirm',
  }) async {
    Route<dynamic>? route;
    var decided = false;
    final answer = await showDialog<bool>(
      context: context,
      barrierDismissible: false,
      builder: (ctx) {
        route = ModalRoute.of(ctx);
        if (route != null) _dialogs.add(route!);
        return AlertDialog(
          title: Text(title),
          content: SingleChildScrollView(
            child: SizedBox(
              width: 560,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: body,
              ),
            ),
          ),
          actions: [
            TextButton(
              key: const ValueKey('ua-review-cancel'),
              onPressed: () {
                if (decided) return;
                decided = true;
                Navigator.pop(ctx, false);
              },
              child: const Text('取消'),
            ),
            FilledButton(
              key: ValueKey(key),
              onPressed: () {
                if (decided) return;
                decided = true;
                Navigator.pop(ctx, true);
              },
              child: const Text('确认执行'),
            ),
          ],
        );
      },
    );
    if (route != null) _dialogs.remove(route);
    return answer == true;
  }

  Future<void> _form(
    String title,
    List<Widget> Function(StateSetter) body,
    String submit,
    Future<bool> Function() apply, {
    String submitKey = 'ua-form-submit',
  }) async {
    String? error;
    var submitting = false;
    Route<dynamic>? route;
    await showDialog<void>(
      context: context,
      barrierDismissible: false,
      builder: (ctx) {
        route = ModalRoute.of(ctx);
        if (route != null) _dialogs.add(route!);
        return StatefulBuilder(
          builder: (ctx, set) => AlertDialog(
            title: Text(title),
            content: SingleChildScrollView(
              child: SizedBox(
                width: 560,
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    ...body(set),
                    if (error != null)
                      Text(
                        error!,
                        key: const ValueKey('ua-form-error'),
                        style: TextStyle(
                          color: Theme.of(ctx).colorScheme.error,
                        ),
                      ),
                  ],
                ),
              ),
            ),
            actions: [
              TextButton(
                onPressed: submitting ? null : () => Navigator.pop(ctx),
                child: const Text('取消'),
              ),
              FilledButton(
                key: ValueKey(submitKey),
                onPressed: submitting
                    ? null
                    : () async {
                        // Finish the native IME connection before replacing this
                        // editor with a nested review dialog. The draft remains
                        // in its controllers when validation or review is cancelled.
                        FocusScope.of(ctx).unfocus();
                        set(() {
                          submitting = true;
                          error = null;
                        });
                        try {
                          final close = await apply();
                          if (ctx.mounted && close) Navigator.pop(ctx);
                        } catch (e) {
                          if (ctx.mounted)
                            set(
                              () => error = e is FormatException
                                  ? e.message
                                  : e.toString(),
                            );
                        } finally {
                          if (ctx.mounted) set(() => submitting = false);
                        }
                      },
                child: Text(submitting ? '正在检查…' : submit),
              ),
            ],
          ),
        );
      },
    );
    if (route != null) _dialogs.remove(route);
  }
}
