import 'dart:async';
import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'opcua_controller.dart';
import 'opcua_models.dart';
import 'opcua_typed_editor.dart';
export 'opcua_models.dart';

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
    if (state == AppLifecycleState.paused ||
        state == AppLifecycleState.detached) {
      // Modal values are memory-only; stale confirmations cannot survive a pause.
      unawaited(model.background());
      for (final route in List<Route<dynamic>>.from(_dialogs).reversed) {
        if (route.isActive) route.navigator?.removeRoute(route);
      }
    } else if (state == AppLifecycleState.resumed) {
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
      final main = SingleChildScrollView(
        key: ValueKey('ua-scroll-$section'),
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            ..._content(node),
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
                        await _details('完整 OPC UA 结果', uaMap(full)['data']);
                    }),
                  ),
              ]),
            if (node.truncated) const Text('结果超出缓存边界或已截断。请缩小范围；未显示的数据不代表不存在。'),
          ],
        ),
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
      case 'attributes':
        return _attributes(node);
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

  List<Widget> _attributes(UaNode node) => [
    if (node.attributes.isEmpty) const Text('刷新会读取全部 27 个标准属性，每一项保留自己的状态与时间。'),
    for (final entry in node.attributes.entries)
      _card(entry.key, [
        _pair('属性 ID', entry.value['attribute_id']),
        _pair('状态', entry.value['status']),
        _pair('类型', entry.value['value_type_name']),
        _pair(
          '值',
          entry.value['value'],
          key: 'ua-attribute-${entry.key}-value',
        ),
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
      ], key: ValueKey('ua-attribute-${entry.key}')),
  ];
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
  Future<void> _jump() async {
    var id = model.current.id;
    await _form(
      '按 NodeId 跳转',
      (set) => [
        _field('NodeId（同一端点）', id, (v) => id = v, key: 'ua-jump-input'),
        const Text('跳转只打开缓存，不访问服务端'),
      ],
      '打开缓存',
      () async {
        uaNodeId(id);
        model.visit(model.current.request, id);
        page = 0;
        return true;
      },
    );
  }

  Future<void> _actions(UaNode node) async {
    await _dialog('节点路径与操作', [
      _pair('节点', node.id),
      _button(
        '读取节点值',
        () => guard(() async {
          await _execute(
            uaOperation(node.request, node.id, 'read'),
            node: node,
          );
        }),
      ),
      _button('编辑节点值', () => _write(node, 'Value')),
      _button(
        '读取唯一浏览路径',
        () => guard(() async {
          await _execute(
            uaOperation(node.request, node.id, 'node-path'),
            node: node,
          );
        }),
      ),
      if (node.path.isNotEmpty) _button('复制缓存路径', () => _copy(node.path)),
      _button('按路径解析节点', () => _path(node)),
      _button('添加独立订阅', () => _subscribe(node)),
    ]);
  }

  Future<void> _path(UaNode node) async {
    var path = node.path.isEmpty ? '/Objects' : node.path;
    await _form(
      '按浏览路径解析',
      (set) => [
        const Text(
          '从 Root 开始，如 /Objects/2:设备/2:温度。名称中的 / 写为 &/，& 写为 &&。多父节点或歧义会报错。',
        ),
        _field('浏览路径', path, (v) => path = v, key: 'ua-path-input'),
      ],
      '明确读取并解析',
      () async {
        if (path.length > 8192) throw const FormatException('路径超过 8192 字符');
        final r = uaOperation(node.request, node.id, 'browse-path');
        r['params'] = {...uaMap(r['params']), 'browse_path': path};
        return _execute(r, node: node);
      },
    );
  }

  Future<void> _referenceFilter(UaNode node) async {
    var direction = '${node.filter['direction'] ?? 'both'}',
        type = '${node.filter['reference_type'] ?? ''}',
        limit = '${node.filter['max_references'] ?? 1000}';
    var subtypes = node.filter['include_subtypes'] != false;
    await _form(
      '引用筛选 · 不联网',
      (set) => [
        _choice(
          '方向',
          ['forward', 'inverse', 'both'],
          direction,
          (v) => set(() => direction = v),
        ),
        _field('引用类型 NodeId（可留空）', type, (v) => type = v),
        _field('最多引用（1–2000）', limit, (v) => limit = v),
        _check('包含子类型', subtypes, (v) => set(() => subtypes = v)),
      ],
      '应用筛选',
      () async {
        if (type.isNotEmpty) uaNodeId(type);
        node.filter = {
          'direction': direction,
          'include_subtypes': subtypes,
          'max_references': uaInteger(limit, 1, 2000),
          if (type.isNotEmpty) 'reference_type': type,
        };
        model.notice = '筛选已改变；点按刷新才读取';
        model.changed();
        return true;
      },
    );
  }

  Future<void> _write(UaNode node, String initialAttribute) async {
    var attribute = initialAttribute;
    UaMap initial(String attr) {
      final key = '${node.key}|$attr', known = node.attributes[attr];
      final fixed = uaAttributeType(attr);
      var kind = fixed.isEmpty
          ? uaType('${known?['value_type_name'] ?? 'String'}')
          : fixed;
      if (!uaTypes.contains(kind.replaceAll('[]', ''))) kind = 'String';
      return model.drafts[key] ??
          {'type': kind, 'value': uaEditable(kind, known?['value'])};
    }

    var draft = initial(attribute);
    await _form(
      '类型化写入草稿',
      (set) => [
        _pair('节点', node.displayId),
        _choice('写入属性', uaWritable, attribute, (v) {
          model.remember('${node.key}|$attribute', draft);
          set(() {
            attribute = v;
            draft = initial(v);
          });
        }, key: 'ua-write-attribute'),
        UaTypedEditor(
          key: ValueKey('ua-write-editor-$attribute'),
          initialType: '${draft['type']}',
          initialValue: draft['value'],
          chooseType: uaAttributeType(attribute).isEmpty,
          fieldKey: 'ua-write-value',
          onChanged: (v) {
            draft = v;
            model.remember('${node.key}|$attribute', draft);
          },
        ),
        const Text('64 位整数保持精确文本。检查和取消不会发送写入。'),
      ],
      '检查确切写入',
      () async {
        model.remember('${node.key}|$attribute', draft);
        final value = uaValidate('${draft['type']}', draft['value']);
        final r = uaOperation(node.request, node.id, 'write');
        r['params'] = {
          ...uaMap(r['params']),
          'attribute': attribute,
          'value_type': draft['type'],
          'value': value,
        };
        return _execute(r, node: node, forceReview: true);
      },
      submitKey: 'ua-preview-write',
    );
    model.remember('${node.key}|$attribute', draft);
  }

  Future<void> _method(UaNode object, String methodId) async {
    final method = model.cache.node(object.request, methodId);
    await _dynamicDialog(
      '方法签名与调用',
      (set) => [
        _pair('所属对象', object.id),
        _pair('方法', methodId),
        const Text('读取签名只读取参数，不调用方法。'),
        _button(
          '只读取方法签名',
          () => guard(() async {
            final r = uaOperation(object.request, methodId, 'method-arguments');
            r['params'] = {...uaMap(r['params']), 'method_id': methodId};
            await _execute(r, node: method);
            set(() {});
          }),
          key: 'ua-fetch-signature',
        ),
        if (method.method != null) ...[
          for (final arg in (method.method!['inputs'] as List? ?? []))
            _pair('输入 ${uaMap(arg)['name']}', uaMap(arg)['type']),
          for (final arg in (method.method!['outputs'] as List? ?? []))
            _pair('输出 ${uaMap(arg)['name']}', uaMap(arg)['type']),
          _button(
            '按签名填写调用',
            () => _methodCall(object, method),
            key: 'ua-open-method-call',
          ),
        ],
        if (method.methodResult != null)
          _card('方法调用结果', [
            _pair('状态', method.methodResult!['status']),
            _pair(
              '输出',
              method.methodResult!['outputs'],
              key: 'ua-method-outputs',
            ),
            _button('完整结果', () => _details('方法调用结果', method.methodResult)),
          ]),
      ],
    );
  }

  Future<void> _methodCall(UaNode object, UaNode method) async {
    final args = (method.method?['inputs'] as List? ?? []).map(uaMap).toList();
    if (args.length > 64) {
      await _details('不支持此签名', '最多 64 个有序参数');
      return;
    }
    for (final arg in args) {
      if (!uaTypes.contains(uaType('${arg['type']}').replaceAll('[]', ''))) {
        await _details('不支持此签名', '自定义结构与多维数组不在现有引擎范围内');
        return;
      }
    }
    final drafts = [
      for (var i = 0; i < args.length; i++)
        model.drafts['${method.key}|arg$i'] ??
            {'type': args[i]['type'], 'value': uaDefault('${args[i]['type']}')},
    ];
    await _form(
      '按签名填写方法参数',
      (set) => [
        _pair('对象', object.id),
        _pair('方法', method.id),
        for (var i = 0; i < args.length; i++)
          _card('${i + 1}. ${args[i]['name']}', [
            Text('${args[i]['description'] ?? ''}'),
            UaTypedEditor(
              key: ValueKey('ua-argument-$i'),
              initialType: '${args[i]['type']}',
              initialValue: drafts[i]['value'],
              fieldKey: 'ua-argument-$i',
              onChanged: (v) {
                drafts[i] = v;
                model.remember('${method.key}|arg$i', v);
              },
            ),
          ]),
        if (args.isEmpty) const Text('此方法没有输入参数，调用仍需明确确认。'),
      ],
      '预览调用',
      () async {
        final values = [
          for (var i = 0; i < args.length; i++)
            {
              'type': args[i]['type'],
              'value': uaValidate('${args[i]['type']}', drafts[i]['value']),
            },
        ];
        final r = uaOperation(method.request, method.id, 'call');
        r['params'] = {
          ...uaMap(r['params']),
          'object_id': object.id,
          'method_id': method.id,
          'arguments': values,
        };
        return _execute(r, node: method, forceReview: true);
      },
      submitKey: 'ua-preview-call',
    );
  }

  Future<void> _subscribe(UaNode node) async {
    var interval = '1000', limit = '1000', duration = '300';
    var reconnect = false;
    await _form(
      '添加独立订阅',
      (set) => [
        _pair('节点', node.displayId),
        _field(
          '发布间隔（50–60000 毫秒）',
          interval,
          (v) => interval = v,
          key: 'ua-sub-interval',
        ),
        _field('最多通知数（1–100000）', limit, (v) => limit = v),
        _field('订阅时限（1–86400 秒）', duration, (v) => duration = v),
        _check('允许断线重建只读订阅', reconnect, (v) => set(() => reconnect = v)),
      ],
      '预览订阅',
      () async {
        final r = uaOperation(node.request, node.id, 'subscribe');
        r['timeout'] = '${uaInteger(duration, 1, 86400)}s';
        r['params'] = {
          ...uaMap(r['params']),
          'interval_ms': uaInteger(interval, 50, 60000),
          'max_events': uaInteger(limit, 1, 100000),
          'auto_reconnect': reconnect,
        };
        final started = await _execute(r, node: node, forceReview: true);
        if (started && mounted) setState(() => section = 'subscriptions');
        return started;
      },
      submitKey: 'ua-preview-subscribe',
    );
  }

  Future<void> _connection(UaMap base, {UaMap? discovered}) async {
    final draft = uaCopy(base), p = uaMap(draft['params']);
    draft['params'] = p;
    var endpoint = '${draft['endpoint'] ?? ''}',
        node = '${p['node_id'] ?? 'i=85'}',
        timeout = '${draft['timeout'] ?? '10s'}';
    var policy = '${p['security_policy'] ?? 'Basic256Sha256'}'.split('#').last,
        mode = uaSecurityMode('${p['security_mode'] ?? 'SignAndEncrypt'}'),
        auth = '${p['auth'] ?? 'anonymous'}';
    final fields = <String, String>{
      for (final k in [
        'username',
        'password',
        'server_cert_sha256',
        'ca_file',
        'cert_file',
        'key_file',
        'auth_cert_file',
        'auth_key_file',
        'application_uri',
      ])
        k: '${p[k] ?? ''}',
    };
    var insecure = p['allow_insecure'] == true,
        legacy = p['allow_legacy_security'] == true;
    var identities = <String>['anonymous', 'username', 'certificate'];
    if (discovered != null) {
      identities = [];
      for (final raw in (discovered['identity_tokens'] as List? ?? [])) {
        final t = '$raw'.toLowerCase();
        if (t.contains('anonymous')) identities.add('anonymous');
        if (t.contains('username')) identities.add('username');
        if (t.contains('certificate')) identities.add('certificate');
      }
      identities = identities.toSet().toList();
      if (identities.isEmpty) {
        await _details('没有受支持的身份方式', '服务端未声明匿名、用户名或证书身份，不会猜测或自动连接');
        return;
      }
      auth = identities.first;
      insecure = false;
      legacy = false;
      fields['server_cert_sha256'] = '';
      fields['ca_file'] = '';
    }
    await _form(
      '连接与安全草稿',
      (set) => [
        if (discovered != null)
          const Text('发现响应尚未认证。不要将显示的证书指纹直接当作信任。请选择身份并独立核对信任。'),
        _field(
          '端点',
          endpoint,
          (v) => endpoint = v,
          key: 'ua-connection-endpoint',
        ),
        _field('初始 NodeId', node, (v) => node = v),
        _field('请求超时', timeout, (v) => timeout = v),
        _choice(
          '安全策略',
          [
            'None',
            'Basic256Sha256',
            'Aes128_Sha256_RsaOaep',
            'Aes256_Sha256_RsaPss',
            'Basic128Rsa15',
            'Basic256',
          ],
          policy,
          (v) => set(() => policy = v),
        ),
        _choice(
          '消息模式',
          [
            'None',
            'Sign',
            'SignAndEncrypt',
            if (!['None', 'Sign', 'SignAndEncrypt'].contains(mode)) mode,
          ],
          mode,
          (v) => set(() => mode = v),
        ),
        _choice('身份方式', identities, auth, (v) => set(() => auth = v)),
        if (auth == 'username') ...[
          _field('用户名', fields['username']!, (v) => fields['username'] = v),
          _field(
            '密码',
            fields['password']!,
            (v) => fields['password'] = v,
            secret: true,
          ),
        ],
        if (auth == 'certificate') ...[
          _field(
            '身份凭据证书路径',
            fields['auth_cert_file']!,
            (v) => fields['auth_cert_file'] = v,
          ),
          _field(
            '身份凭据私钥路径',
            fields['auth_key_file']!,
            (v) => fields['auth_key_file'] = v,
          ),
        ],
        _field(
          '独立核对的服务器 SHA-256 指纹',
          fields['server_cert_sha256']!,
          (v) => fields['server_cert_sha256'] = v,
        ),
        _field(
          '可信 CA 文件（私有相对路径）',
          fields['ca_file']!,
          (v) => fields['ca_file'] = v,
        ),
        _field('客户端证书文件', fields['cert_file']!, (v) => fields['cert_file'] = v),
        _field('客户端私钥文件', fields['key_file']!, (v) => fields['key_file'] = v),
        _field(
          '应用 URI',
          fields['application_uri']!,
          (v) => fields['application_uri'] = v,
        ),
        if (policy == 'None' || mode == 'None')
          _check(
            '明确允许 None 无加密（仅可信测试环境）',
            insecure,
            (v) => set(() => insecure = v),
          ),
        if (['Basic128Rsa15', 'Basic256'].contains(policy))
          _check('明确允许已废弃的旧安全策略', legacy, (v) => set(() => legacy = v)),
        const Text('应用后为只读 browse 草稿，保留原保存配置。不会连接，不会自动保存密码，也不会自动信任发现证书。'),
      ],
      '应用草稿 · 不连接',
      () async {
        final uri = Uri.tryParse(endpoint);
        if (uri == null ||
            uri.scheme != 'opc.tcp' ||
            uri.host.isEmpty ||
            uri.userInfo.isNotEmpty)
          throw const FormatException('请输入不含凭据的 opc.tcp 端点');
        uaNodeId(node);
        if (!['None', 'Sign', 'SignAndEncrypt'].contains(mode)) {
          throw const FormatException('不支持的消息安全模式；请明确选择受支持模式');
        }
        if ((policy == 'None' || mode == 'None') && !insecure)
          throw const FormatException('必须明确勾选 None 安全例外');
        if (['Basic128Rsa15', 'Basic256'].contains(policy) && !legacy)
          throw const FormatException('必须明确勾选旧策略例外');
        final pin = fields['server_cert_sha256']!.replaceAll(':', '');
        if (pin.isNotEmpty && !RegExp(r'^[0-9a-fA-F]{64}$').hasMatch(pin))
          throw const FormatException('SHA-256 指纹需要 64 位十六进制');
        draft['action'] = 'browse';
        draft['endpoint'] = endpoint;
        draft['timeout'] = timeout;
        draft['params'] = {
          'node_id': node,
          'security_policy': policy,
          'security_mode': mode,
          'auth': auth,
          if (insecure && (policy == 'None' || mode == 'None'))
            'allow_insecure': true,
          if (legacy && ['Basic128Rsa15', 'Basic256'].contains(policy))
            'allow_legacy_security': true,
          for (final entry in fields.entries)
            if (entry.value.isNotEmpty &&
                (!(entry.key == 'username' || entry.key == 'password') ||
                    auth == 'username') &&
                (!(entry.key == 'auth_cert_file' ||
                        entry.key == 'auth_key_file') ||
                    auth == 'certificate'))
              entry.key: entry.value,
        };
        widget.onPrepare(uaCopy(draft));
        model.visit(draft, node);
        return true;
      },
      submitKey: 'ua-apply-connection',
    );
  }

  Future<void> _connections() async {
    await guard(() async {
      await model.refreshConnections();
      if (!mounted) return;
      await _dynamicDialog(
        '成功连接历史',
        (set) => [
          const Text('仅保存端点、节点、安全策略与连接时间，不保存用户名或密码。选择仅生成可编辑草稿。'),
          for (final row in model.connections)
            _card(uaSafeEndpoint(row['endpoint']), [
              _pair('节点', row['node_id']),
              _pair('最后成功连接', row['last_connected']),
              _pair('安全策略', row['security_policy']),
              _button(
                '选择并检查连接草稿',
                () => _connection({
                  'id':
                      'opcua-history-${DateTime.now().millisecondsSinceEpoch}',
                  'name': '最近连接 · 待核对',
                  'protocol': 'opcua',
                  'action': 'browse',
                  'endpoint': row['endpoint'],
                  'timeout': '10s',
                  'params': {
                    for (final k in [
                      'node_id',
                      'security_policy',
                      'security_mode',
                      'server_cert_sha256',
                    ])
                      if (row[k] != null) k: row[k],
                  },
                }),
              ),
            ]),
          _button(
            '清除连接历史',
            () => guard(() async {
              if (await _confirm('清除本机连接记录', [
                const Text('不会修改服务端，也不会删除请求。'),
              ])) {
                await model.clearConnections();
                set(() {});
              }
            }),
          ),
        ],
      );
    });
  }

  Future<void> _identity() async {
    final suffix = DateTime.now().millisecondsSinceEpoch;
    var cert = 'opcua-client-$suffix.crt',
        key = 'opcua-client-$suffix.key',
        uri = 'urn:iotools:mobile:client';
    await _form(
      '生成 OPC UA 客户端身份',
      (set) => [
        _field('新证书文件（私有相对路径）', cert, (v) => cert = v),
        _field('新私钥文件（私有相对路径）', key, (v) => key = v),
        _field('应用 URI', uri, (v) => uri = v),
        const Text('只创建新的私有文件，不覆盖已有文件，不上传，不自动建立服务器信任。取消会中止生成。'),
        if (model.identity != null) _pair('上次生成结果', model.identity),
      ],
      '检查生成目标',
      () async {
        for (final path in [cert, key]) {
          if (path.isEmpty ||
              path.startsWith('/') ||
              path.split('/').contains('..') ||
              path.contains('://'))
            throw const FormatException('请使用新的私有相对路径');
        }
        if (cert == key) throw const FormatException('证书与私钥路径必须不同');
        if (Uri.tryParse(uri)?.hasScheme != true)
          throw const FormatException('应用 URI 必须包含 scheme');
        if (!await _confirm('生成新的证书与私钥', [
          _pair('证书', cert),
          _pair('私钥', key),
          _pair('应用 URI', uri),
        ], key: 'ua-confirm-identity'))
          return false;
        await model.generateIdentity(cert, key, uri);
        return true;
      },
      submitKey: 'ua-preview-identity',
    );
  }

  Future<void> _logs() async {
    await _dynamicDialog(
      'OPC UA 活动记录',
      (set) => [
        const Text('最近 100 条内存状态；不保存凭据或通知内容。独立滚动，不影响节点缓存。'),
        for (final line in model.log)
          Padding(
            padding: const EdgeInsets.only(bottom: 8),
            child: SelectableText(line),
          ),
        if (model.identity != null)
          _card('身份生成结果', [_pair('私有文件与应用 URI', model.identity)]),
        _button('清除本机活动显示', () {
          model.log.clear();
          set(() {});
        }),
        if (widget.exportText != null)
          _button(
            '明确导出活动文本',
            () => guard(
              () => widget.exportText!(
                'opcua-activity.txt',
                model.log.join('\n'),
              ),
            ),
          ),
      ],
    );
  }

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
