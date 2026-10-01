import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_localizations/flutter_localizations.dart';
import '../core/engine.dart';
import '../core/json.dart';
import '../core/session.dart';
import '../shared/widgets.dart';
import '../features/requests/request_form.dart';
import '../features/http/http_tools.dart';
import '../features/history/history_page.dart';
import '../features/messaging/messaging.dart';
import '../features/modbus/modbus_workspace.dart';
import '../features/opcua/opcua_workspace.dart';

class IotoolsApp extends StatefulWidget {
  const IotoolsApp({required this.engine, required this.platform, super.key});
  final Engine engine;
  final PlatformServices platform;
  @override
  State<IotoolsApp> createState() => _IotoolsAppState();
}

class _IotoolsAppState extends State<IotoolsApp> {
  late final AppSession session;
  ThemeMode mode = ThemeMode.dark;
  @override
  void initState() {
    super.initState();
    session = AppSession(widget.engine, widget.platform);
    session.initialize().then((_) {
      if (mounted)
        setState(
          () => mode = ThemeMode.values.firstWhere(
            (v) => v.name == session.preferences['theme'],
            orElse: () => ThemeMode.dark,
          ),
        );
    });
  }

  @override
  void dispose() {
    session.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => MaterialApp(
    title: 'iotools',
    debugShowCheckedModeBanner: false,
    theme: appTheme(Brightness.light),
    darkTheme: appTheme(Brightness.dark),
    themeMode: mode,
    locale: const Locale('zh', 'CN'),
    supportedLocales: const [Locale('zh', 'CN'), Locale('en')],
    localizationsDelegates: GlobalMaterialLocalizations.delegates,
    home: WorkspaceShell(
      session: session,
      onTheme: (v) {
        setState(() => mode = v);
        session.platform.invoke('settings.save', {'theme': v.name});
      },
      themeMode: mode,
    ),
  );
}

class WorkspaceShell extends StatefulWidget {
  const WorkspaceShell({
    required this.session,
    required this.onTheme,
    required this.themeMode,
    super.key,
  });
  final AppSession session;
  final ValueChanged<ThemeMode> onTheme;
  final ThemeMode themeMode;
  @override
  State<WorkspaceShell> createState() => _WorkspaceShellState();
}

class _WorkspaceShellState extends State<WorkspaceShell>
    with WidgetsBindingObserver {
  AppSession get s => widget.session;
  int nav = 0, tab = 0, generation = 0;
  bool editor = false, busy = false, dialogOpen = false;
  String filter = '', search = '';
  late StreamSubscription<JsonMap> subscription;
  final yaml = TextEditingController();
  BuildContext? interactionContext;
  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    subscription = s.eventStream.listen(interaction);
  }

  @override
  void dispose() {
    WidgetsBinding.instance.removeObserver(this);
    subscription.cancel();
    yaml.dispose();
    super.dispose();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.paused ||
        state == AppLifecycleState.detached) {
      if (interactionContext != null) Navigator.of(interactionContext!).pop();
      s.pause();
    } else if (state == AppLifecycleState.resumed) {
      s.resume();
    }
  }

  Future<void> interaction(JsonMap event) async {
    if (event['kind'] != 'interaction' || !mounted || s.paused) return;
    final data = mapOf(event['data']);
    final epoch = s.epoch;
    dialogOpen = true;
    Object? value;
    int? index;
    bool accepted = false;
    final type = data['type'];
    final text = TextEditingController(text: data['default']?.toString() ?? '');
    await memoryDialog(
      context: context,
      barrierDismissible: false,
      builder: (c) {
        interactionContext = c;
        return StatefulBuilder(
          builder: (c, set) => AlertDialog(
            title: Text(data['title']?.toString() ?? '执行请求'),
            content: SizedBox(
              width: 580,
              child: SingleChildScrollView(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.stretch,
                  children: [
                    if (data['request'] != null) DataView(data['request']),
                    if (type == 'prompt')
                      TextField(
                        controller: text,
                        autofocus: true,
                        obscureText: data['sensitive'] == true,
                        decoration: const InputDecoration(labelText: '输入值'),
                      ),
                    if (type == 'select')
                      ...(data['options'] as List? ?? []).asMap().entries.map(
                        (e) => ListTile(
                          title: Text(
                            e.value is String
                                ? '"${e.value}"'
                                : pretty(e.value),
                          ),
                          leading: Icon(
                            index == e.key
                                ? Icons.radio_button_checked
                                : Icons.radio_button_off,
                          ),
                          onTap: () => set(() => index = e.key),
                        ),
                      ),
                  ],
                ),
              ),
            ),
            actions: [
              TextButton(
                onPressed: () => Navigator.pop(c),
                child: const Text('取消'),
              ),
              FilledButton(
                onPressed: type == 'select' && index == null
                    ? null
                    : () {
                        accepted = true;
                        value = text.text;
                        Navigator.pop(c);
                      },
                child: const Text('确认'),
              ),
            ],
          ),
        );
      },
    );
    interactionContext = null;
    dialogOpen = false;
    text.dispose();
    if (!mounted || s.paused || epoch != s.epoch) return;
    await guarded(
      context,
      () => s.command({
        'op': 'respond',
        'interaction_id': data['interaction_id'],
        'confirmed': accepted,
        if (type == 'select' && accepted) 'selection_index': index,
        if (type == 'prompt' && accepted) 'value': value,
      }),
    );
  }

  Future<void> reviewAndRun(JsonMap request, {JsonMap? overrides}) async {
    if (busy) return;
    setState(() => busy = true);
    final epoch = s.epoch;
    try {
      final p = await s.preview(request, overrides: overrides);
      if (!mounted || s.paused || epoch != s.epoch) return;
      bool accepted = true;
      if (p['confirmation_required'] == true) {
        accepted = await confirm(
          context,
          '确认执行写操作',
          '${p['summary'] ?? ''}\n\n${pretty(p['protocol_preview'] ?? p['request'])}\n\n${(p['warnings'] as List? ?? []).join('\n')}',
          action: '确认执行',
        );
      }
      if (!accepted || !mounted || s.paused || epoch != s.epoch) return;
      await s.run(request, p, confirmed: accepted, overrides: overrides);
      if (mounted)
        setState(() {
          editor = true;
          nav = 0;
          tab = 2;
        });
    } catch (e) {
      if (mounted) reportError(context, e);
    } finally {
      if (mounted) setState(() => busy = false);
    }
  }

  void prepare(JsonMap request) {
    s.prepare(request);
    setState(() {
      editor = true;
      nav = 0;
      tab = 0;
      generation++;
    });
  }

  void openRequest(JsonMap request) {
    s.choose(request);
    yaml.text = s.source;
    setState(() {
      editor = true;
      tab = 0;
      generation++;
    });
  }

  Future<void> newRequest() async {
    final protocols = rowsOf(s.catalog['protocols']);
    if (protocols.isEmpty) return;
    final p = await showModalBottomSheet<JsonMap>(
      context: context,
      showDragHandle: true,
      builder: (c) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: protocols
              .map(
                (p) => ListTile(
                  leading: const Icon(Icons.add_circle_outline),
                  title: Text(p['name'].toString()),
                  onTap: () => Navigator.pop(c, p),
                ),
              )
              .toList(),
        ),
      ),
    );
    if (p == null) return;
    final a = rowsOf(p['actions']).first;
    prepare({
      'id': 'request_${DateTime.now().millisecondsSinceEpoch}',
      'name': '新建 ${p['name']}',
      'protocol': p['id'],
      'action': a['id'],
      'endpoint': '',
      'timeout': '10s',
      'params': cloneMap(mapOf(a['defaults'])),
    });
  }

  @override
  Widget build(BuildContext context) => AnimatedBuilder(
    animation: s,
    builder: (context, _) {
      if (!s.ready) {
        return Scaffold(
          appBar: AppBar(title: const Text('iotools')),
          body: s.error == null
              ? const Center(child: CircularProgressIndicator())
              : EmptyState('无法打开配置', s.error!),
        );
      }
      final titles = ['请求工作台', '执行历史', '设置'];
      return PopScope(
        canPop: !editor || nav != 0,
        onPopInvokedWithResult: (didPop, result) {
          if (!didPop) setState(() => editor = false);
        },
        child: Scaffold(
          appBar: AppBar(
            leading: nav == 0 && editor
                ? IconButton(
                    tooltip: '返回请求列表',
                    onPressed: () => setState(() => editor = false),
                    icon: const Icon(Icons.arrow_back),
                  )
                : null,
            title: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  editor && nav == 0
                      ? s.draft['name']?.toString() ?? '请求'
                      : titles[nav],
                ),
                Text(
                  'iotools · ${s.readOnly ? '只读模式' : '可写模式'}',
                  style: Theme.of(context).textTheme.labelSmall,
                ),
              ],
            ),
            actions: [
              if (s.running)
                IconButton(
                  tooltip: '取消任务',
                  onPressed: () => guarded(context, s.cancel),
                  icon: const Icon(Icons.stop_circle_outlined),
                ),
              if (nav == 0 && !editor)
                IconButton(
                  tooltip: '新建请求',
                  onPressed: newRequest,
                  icon: const Icon(Icons.add),
                ),
            ],
          ),
          body: SafeArea(
            child: nav == 0
                ? (editor ? workbench() : requestList())
                : nav == 1
                ? HistoryPage(session: s, onPrepare: prepare)
                : settings(),
          ),
          bottomNavigationBar: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (nav == 0 && editor) actionBar(),
              NavigationBar(
                selectedIndex: nav,
                onDestinationSelected: (v) => setState(() => nav = v),
                destinations: const [
                  NavigationDestination(
                    key: ValueKey('nav_requests'),
                    icon: Icon(Icons.dashboard_outlined),
                    selectedIcon: Icon(Icons.dashboard),
                    label: '请求',
                  ),
                  NavigationDestination(
                    key: ValueKey('nav_history'),
                    icon: Icon(Icons.history),
                    label: '历史',
                  ),
                  NavigationDestination(
                    key: ValueKey('nav_settings'),
                    icon: Icon(Icons.settings_outlined),
                    label: '设置',
                  ),
                ],
              ),
            ],
          ),
        ),
      );
    },
  );
  Widget requestList() {
    final all = {
      for (final r in s.requests) r['id'].toString(): r,
      ...s.drafts,
    };
    final list = all.values
        .where(
          (r) =>
              (filter.isEmpty || r['protocol'] == filter) &&
              '${r['name']} ${r['id']} ${r['endpoint']}'.toLowerCase().contains(
                search.toLowerCase(),
              ),
        )
        .toList();
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
          child: TextField(
            key: const ValueKey('request_search'),
            decoration: const InputDecoration(
              prefixIcon: Icon(Icons.search),
              hintText: '搜索请求、地址或名称',
            ),
            onChanged: (v) => setState(() => search = v),
          ),
        ),
        SingleChildScrollView(
          scrollDirection: Axis.horizontal,
          child: Padding(
            padding: const EdgeInsets.all(12),
            child: Wrap(
              spacing: 8,
              children: ['', 'http', 'mqtt', 'kafka', 'modbus', 'opcua']
                  .map(
                    (p) => FilterChip(
                      label: Text(p.isEmpty ? '全部' : p.toUpperCase()),
                      selected: filter == p,
                      onSelected: (_) => setState(() => filter = p),
                    ),
                  )
                  .toList(),
            ),
          ),
        ),
        if ((s.state['profiles'] as List? ?? []).isNotEmpty)
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            child: ChoiceField(
              label: '环境变量配置',
              value: s.state['profile']?.toString() ?? '',
              options: (s.state['profiles'] as List)
                  .map((e) => e.toString())
                  .toList(),
              onChanged: (v) => guarded(context, () => s.setProfile(v)),
            ),
          ),
        Expanded(
          child: list.isEmpty
              ? const EmptyState('还没有请求', '点击右上角 +，选择协议并创建请求')
              : LayoutBuilder(
                  builder: (c, box) => GridView.builder(
                    padding: const EdgeInsets.all(16),
                    gridDelegate: SliverGridDelegateWithFixedCrossAxisCount(
                      crossAxisCount: box.maxWidth > 850
                          ? 3
                          : box.maxWidth > 580
                          ? 2
                          : 1,
                      mainAxisExtent: 154,
                      crossAxisSpacing: 12,
                    ),
                    itemCount: list.length,
                    itemBuilder: (c, i) {
                      final r = list[i];
                      return Card(
                        child: InkWell(
                          borderRadius: BorderRadius.circular(18),
                          onTap: () => openRequest(r),
                          child: Padding(
                            padding: const EdgeInsets.all(16),
                            child: Column(
                              crossAxisAlignment: CrossAxisAlignment.start,
                              children: [
                                Row(
                                  children: [
                                    Container(
                                      padding: const EdgeInsets.symmetric(
                                        horizontal: 10,
                                        vertical: 5,
                                      ),
                                      decoration: BoxDecoration(
                                        color: cyan.withValues(alpha: .15),
                                        borderRadius: BorderRadius.circular(8),
                                      ),
                                      child: Text(
                                        r['protocol'].toString().toUpperCase(),
                                        style: const TextStyle(
                                          color: cyan,
                                          fontWeight: FontWeight.bold,
                                        ),
                                      ),
                                    ),
                                    const Spacer(),
                                    if (s.drafts.containsKey(r['id']))
                                      const Text(
                                        '未保存',
                                        style: TextStyle(color: Colors.amber),
                                      ),
                                    PopupMenuButton<String>(
                                      onSelected: (v) => cardAction(v, r),
                                      itemBuilder: (c) => const [
                                        PopupMenuItem(
                                          value: 'copy',
                                          child: Text('复制为新请求'),
                                        ),
                                        PopupMenuItem(
                                          value: 'delete',
                                          child: Text('删除请求'),
                                        ),
                                      ],
                                    ),
                                  ],
                                ),
                                Text(
                                  r['name']?.toString().isNotEmpty == true
                                      ? r['name'].toString()
                                      : r['id'].toString(),
                                  style: Theme.of(c).textTheme.titleMedium,
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                ),
                                const SizedBox(height: 5),
                                Text(
                                  '${r['action']}  ${redactedEndpoint(r['endpoint']?.toString() ?? '')}',
                                  maxLines: 2,
                                  overflow: TextOverflow.ellipsis,
                                  style: Theme.of(c).textTheme.bodySmall,
                                ),
                              ],
                            ),
                          ),
                        ),
                      );
                    },
                  ),
                ),
        ),
      ],
    );
  }

  Future<void> cardAction(String action, JsonMap r) async {
    if (action == 'copy') {
      final copy = cloneMap(r);
      copy['id'] = '${r['id']}_${DateTime.now().millisecondsSinceEpoch}';
      copy['name'] = '${r['name'] ?? r['id']} 副本';
      prepare(copy);
      return;
    }
    if (await confirm(
          context,
          '删除请求？',
          '只删除本地配置中的 ${r['name'] ?? r['id']}，不会向服务发送请求',
          action: '删除',
        ) &&
        mounted) {
      await guarded(context, () async {
        s.stateChanged(
          mapOf(
            await s.command({
              'op': 'request.delete',
              'request_id': r['id'],
              'confirmed': true,
            }),
          ),
        );
        s.drafts.remove(r['id']);
        await s.refreshSource();
      });
    }
  }

  Widget workbench() => Column(
    children: [
      SingleChildScrollView(
        scrollDirection: Axis.horizontal,
        child: Padding(
          padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 8),
          child: SegmentedButton<int>(
            segments: const [
              ButtonSegment(
                value: 0,
                label: Text('表单'),
                icon: Icon(Icons.tune),
              ),
              ButtonSegment(
                value: 1,
                label: Text('YAML'),
                icon: Icon(Icons.code),
              ),
              ButtonSegment(
                value: 2,
                label: Text('结果'),
                icon: Icon(Icons.receipt_long),
              ),
              ButtonSegment(
                value: 3,
                label: Text('工具'),
                icon: Icon(Icons.build_outlined),
              ),
            ],
            selected: {tab},
            onSelectionChanged: (v) {
              if (v.first == 1 && yaml.text.isEmpty) yaml.text = s.source;
              setState(() => tab = v.first);
            },
          ),
        ),
      ),
      Expanded(
        child: switch (tab) {
          0 => ListView(
            padding: const EdgeInsets.symmetric(horizontal: 16),
            children: [
              RequestForm(
                key: ValueKey('${s.draft['id']}_$generation'),
                session: s,
                request: s.draft,
                onChanged: s.updateDraft,
              ),
            ],
          ),
          1 => Padding(
            padding: const EdgeInsets.all(16),
            child: TextField(
              key: const ValueKey('yaml_editor'),
              controller: yaml,
              expands: true,
              minLines: null,
              maxLines: null,
              style: const TextStyle(fontFamily: 'monospace', fontSize: 14),
              keyboardType: TextInputType.multiline,
              decoration: const InputDecoration(
                labelText: '完整集合 YAML',
                alignLabelWithHint: true,
              ),
              onChanged: (v) => s.source = v,
            ),
          ),
          2 => ResultPage(
            session: s,
            onPrepare: prepare,
            onRerun: () => reviewAndRun(
              s.originalResultRequest ?? s.draft,
              overrides: s.resultOverrides,
            ),
          ),
          _ => toolsPage(),
        },
      ),
    ],
  );
  Widget actionBar() => Container(
    padding: const EdgeInsets.fromLTRB(16, 8, 16, 8),
    decoration: BoxDecoration(
      color: Theme.of(context).colorScheme.surface,
      border: Border(top: BorderSide(color: cyan.withValues(alpha: .15))),
    ),
    child: Row(
      children: [
        if (tab == 2)
          Expanded(child: Text(s.status))
        else
          Expanded(
            child: OutlinedButton.icon(
              key: const ValueKey('save_request'),
              onPressed: busy
                  ? null
                  : () => guarded(context, () async {
                      if (tab == 1) {
                        s.source = yaml.text;
                        await s.saveSource();
                      } else {
                        await s.saveRequest(s.draft);
                      }
                      if (mounted)
                        ScaffoldMessenger.of(
                          context,
                        ).showSnackBar(const SnackBar(content: Text('已保存')));
                    }),
              icon: const Icon(Icons.save_outlined),
              label: const Text('保存'),
            ),
          ),
        const SizedBox(width: 12),
        Expanded(
          child: FilledButton.icon(
            key: const ValueKey('run_request'),
            onPressed: busy
                ? null
                : s.running
                ? () => guarded(context, s.cancel)
                : () => reviewAndRun(
                    tab == 2 ? s.originalResultRequest ?? s.draft : s.draft,
                    overrides: tab == 2 ? s.resultOverrides : null,
                  ),
            icon: Icon(s.running ? Icons.stop : Icons.play_arrow),
            label: Text(
              busy
                  ? '准备中'
                  : s.running
                  ? '取消'
                  : tab == 2
                  ? '重新执行'
                  : '执行',
            ),
          ),
        ),
      ],
    ),
  );
  Widget toolsPage() => ListView(
    padding: const EdgeInsets.all(16),
    children: [
      if (s.draft['protocol'] == 'http')
        HttpTools(
          session: s,
          onRun: (r, o) => reviewAndRun(r, overrides: o),
        ),
      if (s.draft['protocol'] == 'mqtt' || s.draft['protocol'] == 'kafka')
        MessagingTools(session: s, onPrepare: prepare),
      if (s.draft['protocol'] == 'modbus')
        Section(
          'Modbus 工作区',
          children: [
            FilledButton.icon(
              onPressed: () => Navigator.push(
                context,
                MaterialPageRoute(
                  builder: (_) => ModbusWorkspace(
                    host: _ModbusAdapter(s, reviewAndRun, prepare),
                  ),
                ),
              ),
              icon: const Icon(Icons.grid_on),
              label: const Text('寄存器、采样与高级工具'),
            ),
          ],
        ),
      if (s.draft['protocol'] == 'opcua')
        Section(
          'OPC UA 工作区',
          children: [
            FilledButton.icon(
              key: const ValueKey('open_opcua_workspace'),
              onPressed: () => Navigator.push(
                context,
                MaterialPageRoute(
                  builder: (_) => OpcuaWorkspace(
                    request: cloneMap(s.draft),
                    command: s.command,
                    events: s.eventStream,
                    readOnly: s.readOnly,
                    onPrepare: prepare,
                    onStarted: s.started,
                    exportText: s.exportText,
                  ),
                ),
              ),
              icon: const Icon(Icons.account_tree_outlined),
              label: const Text('节点浏览、属性、方法与订阅'),
            ),
          ],
        ),
      Section(
        '本地配置',
        children: [
          OutlinedButton(
            onPressed: () => guarded(context, () async {
              await s.command({'op': 'config.validate', 'source': s.source});
              if (mounted) showData(context, '校验通过', '配置有效；未保存或执行');
            }),
            child: const Text('校验配置'),
          ),
          OutlinedButton(
            onPressed: () =>
                guarded(context, () => s.exportText('iotools.yaml', s.source)),
            child: const Text('导出当前 YAML'),
          ),
        ],
      ),
    ],
  );
  Widget settings() => ListView(
    padding: const EdgeInsets.all(16),
    children: [
      Section(
        '运行与隐私',
        children: [
          SwitchListTile(
            key: const ValueKey('read_only'),
            title: const Text('只读保护'),
            subtitle: const Text('共享内核拒绝所有协议写操作和历史变更'),
            value: s.readOnly,
            onChanged: s.running
                ? null
                : (v) => guarded(context, () => s.setOptions(readOnly: v)),
          ),
          SwitchListTile(
            key: const ValueKey('history_opt_in'),
            title: const Text('记录 HTTP 历史'),
            subtitle: const Text('开启后将响应和请求元数据写入应用私有数据库'),
            value: s.history,
            onChanged: s.running
                ? null
                : (v) async {
                    if (!v ||
                        await confirm(
                          context,
                          '记录 HTTP 历史？',
                          '响应内容可能包含敏感信息，将保存在本机应用私有目录。',
                          action: '开启',
                        )) {
                      if (mounted)
                        guarded(context, () => s.setOptions(history: v));
                    }
                  },
          ),
          ChoiceField(
            label: '外观',
            value: widget.themeMode.name,
            options: const ['dark', 'light', 'system'],
            onChanged: (v) =>
                widget.onTheme(ThemeMode.values.firstWhere((t) => t.name == v)),
          ),
        ],
      ),
      Section(
        '配置与文件',
        subtitle: s.state['path']?.toString(),
        children: [
          OutlinedButton(
            key: const ValueKey('open_yaml'),
            onPressed: () {
              yaml.text = s.source;
              if (s.draft.isEmpty && s.requests.isNotEmpty)
                s.choose(s.requests.first);
              setState(() {
                nav = 0;
                editor = true;
                tab = 1;
              });
            },
            child: const Text('编辑完整 YAML'),
          ),
          OutlinedButton(
            onPressed: () => importConfig(),
            child: const Text('导入配置 / 转换格式'),
          ),
          OutlinedButton(
            onPressed: () => importBundle(),
            child: const Text('导入 ZIP 配置与附件'),
          ),
          OutlinedButton(
            onPressed: () => fileInventory(),
            child: const Text('私有文件 / 切换集合'),
          ),
          OutlinedButton(
            onPressed: () =>
                guarded(context, () => s.exportText('iotools.yaml', s.source)),
            child: const Text('导出 YAML'),
          ),
          OutlinedButton(
            onPressed: () async {
              if (await confirm(
                    context,
                    '重新载入配置？',
                    '当前 YAML 编辑器内容将由磁盘版本替换；表单草稿保留。',
                  ) &&
                  mounted)
                guarded(context, () async {
                  s.stateChanged(
                    mapOf(await s.command({'op': 'config.reload'})),
                  );
                  await s.refreshSource();
                  yaml.text = s.source;
                });
            },
            child: const Text('重新载入磁盘配置'),
          ),
        ],
      ),
      Section(
        'USB 串口',
        children: [
          OutlinedButton(
            onPressed: () => usbDialog(),
            child: const Text('选择设备与串口参数'),
          ),
          const Text('需要真实适配器完成硬件验收；后台会关闭连接，返回不会自动重连。'),
        ],
      ),
      Section(
        '关于',
        children: [
          SelectableText(s.state['version']?.toString() ?? '版本未知'),
          const Text('Flutter 触控界面 · 共享 Go 协议内核'),
          OutlinedButton(
            onPressed: () => guarded(context, () async {
              final help = await s.platform.invoke('help.read');
              if (mounted) showData(context, '中文操作帮助', help);
            }),
            child: const Text('中文操作帮助'),
          ),
          OutlinedButton(
            onPressed: () => guarded(context, () async {
              final license = await s.platform.invoke('licenses.read');
              if (mounted) showData(context, '许可证', license);
            }),
            child: const Text('项目许可证'),
          ),
          OutlinedButton(
            onPressed: () =>
                showLicensePage(context: context, applicationName: 'iotools'),
            child: const Text('开源许可'),
          ),
        ],
      ),
    ],
  );
  Future<void> importConfig() async {
    await guarded(context, () async {
      final f = mapOf(
        await s.platform.invoke('files.pick', {'limit': 4194304}),
      );
      if (f.isEmpty) return;
      final content = await s.platform.invoke('files.read', {
        'path': f['path'],
        'limit': 4194304,
      });
      if (!mounted) return;
      String format = 'iotools';
      final ok = await memoryDialog<bool>(
        context: context,
        builder: (c) => AlertDialog(
          title: const Text('选择配置格式'),
          content: ChoiceField(
            label: '格式',
            value: format,
            options: const [
              'iotools',
              'slumber',
              'v3',
              'rest',
              'openapi',
              'insomnia',
            ],
            onChanged: (v) => format = v,
          ),
          actions: [
            TextButton(
              onPressed: () => Navigator.pop(c),
              child: const Text('取消'),
            ),
            FilledButton(
              onPressed: () => Navigator.pop(c, true),
              child: const Text('预览转换'),
            ),
          ],
        ),
      );
      if (ok != true) return;
      final result = mapOf(
        await s.command({
          'op': 'config.import',
          'source': content is String ? content : mapOf(content)['text'],
          'format': format,
        }),
      );
      if (!mounted) return;
      if (await confirm(
        context,
        '应用到 YAML 草稿？',
        '转换结果只放入编辑器；点击保存才会写入配置。\n${pretty(result['collection'])}',
        action: '放入草稿',
      )) {
        yaml.text = result['source'].toString();
        s.source = yaml.text;
        setState(() {
          nav = 0;
          editor = true;
          tab = 1;
        });
      }
    });
  }

  Future<void> importBundle() async {
    await guarded(context, () async {
      final result = mapOf(await s.platform.invoke('files.importBundle'));
      if (result.isEmpty || !mounted) return;
      await showData(context, '已导入的私有文件', result);
      await fileInventory();
    });
  }

  Future<void> fileInventory() async {
    await guarded(context, () async {
      final files = rowsOf(await s.command({'op': 'files.list'}));
      if (!mounted) return;
      await showModalBottomSheet(
        context: context,
        isScrollControlled: true,
        showDragHandle: true,
        builder: (c) => DraggableScrollableSheet(
          expand: false,
          initialChildSize: .8,
          builder: (c, scroll) => ListView(
            controller: scroll,
            children: [
              const ListTile(title: Text('应用私有文件')),
              for (final f in files)
                ListTile(
                  title: Text(f['path']?.toString() ?? f['name'].toString()),
                  subtitle: Text('${f['bytes']} 字节'),
                  trailing: PopupMenuButton<String>(
                    onSelected: (v) => guarded(context, () async {
                      if (v == 'export') {
                        await s.platform.invoke('files.export', {
                          'path': f['path'],
                          'name': f['name'],
                        });
                      } else {
                        final p = mapOf(
                          await s.command({
                            'op': 'config.switch',
                            'path': f['path'],
                            'confirmed': false,
                          }),
                        );
                        if (!mounted) return;
                        if (await confirm(
                          context,
                          '切换集合？',
                          '未保存草稿仍留在内存中。\n${pretty(p['collection'])}',
                          action: '切换',
                        )) {
                          s.stateChanged(
                            mapOf(
                              await s.command({
                                'op': 'config.switch',
                                'path': f['path'],
                                'token': p['token'],
                                'confirmed': true,
                              }),
                            ),
                          );
                          await s.refreshSource();
                          yaml.text = s.source;
                          if (mounted) setState(() => editor = false);
                        }
                      }
                    }),
                    itemBuilder: (c) => const [
                      PopupMenuItem(value: 'export', child: Text('导出文件')),
                      PopupMenuItem(value: 'switch', child: Text('预览为配置集合')),
                    ],
                  ),
                ),
            ],
          ),
        ),
      );
    });
  }

  Future<void> usbDialog() async {
    await guarded(context, () async {
      final raw = await s.platform.invoke('usb.list');
      final ports = rowsOf(raw is String ? exactDecode(raw) : raw);
      if (!mounted) return;
      String endpoint = ports.isEmpty ? '' : ports.first['endpoint'].toString(),
          parity = 'N';
      final baud = TextEditingController(text: '9600'),
          stop = TextEditingController(text: '1');
      await memoryDialog(
        context: context,
        builder: (c) => AlertDialog(
          title: const Text('USB 串口'),
          content: Column(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (ports.isEmpty) const Text('未发现支持的 USB 串口设备'),
              if (ports.isNotEmpty)
                ChoiceField(
                  label: '设备 / 端口',
                  value: endpoint,
                  options: ports.map((p) => p['endpoint'].toString()).toList(),
                  onChanged: (v) => endpoint = v,
                ),
              TextField(
                controller: baud,
                keyboardType: TextInputType.number,
                decoration: const InputDecoration(labelText: '波特率'),
              ),
              TextField(
                controller: stop,
                keyboardType: TextInputType.number,
                decoration: const InputDecoration(labelText: '停止位 1 / 2'),
              ),
              ChoiceField(
                label: '校验',
                value: parity,
                options: const ['N', 'E', 'O'],
                onChanged: (v) => parity = v,
              ),
            ],
          ),
          actions: [
            TextButton(
              onPressed: () => guarded(c, () => s.platform.invoke('usb.close')),
              child: const Text('关闭连接'),
            ),
            TextButton(
              onPressed: ports.isEmpty
                  ? null
                  : () => guarded(
                      c,
                      () => s.platform.invoke('usb.permission', {
                        'endpoint': endpoint,
                      }),
                    ),
              child: const Text('申请设备权限'),
            ),
            FilledButton(
              onPressed: ports.isEmpty
                  ? null
                  : () => guarded(c, () async {
                      await s.platform.invoke('usb.open', {
                        'endpoint': endpoint,
                        'baud': int.parse(baud.text),
                        'dataBits': 8,
                        'stopBits': int.parse(stop.text),
                        'parity': parity,
                      });
                      final r = cloneMap(s.draft);
                      r['endpoint'] = endpoint;
                      r['params'] = {
                        ...mapOf(r['params']),
                        'baud': baud.text,
                        'data_bits': 8,
                        'stop_bits': stop.text,
                        'parity': parity,
                      };
                      prepare(r);
                      if (c.mounted) Navigator.pop(c);
                    }),
              child: const Text('打开并放入草稿'),
            ),
          ],
        ),
      );
      baud.dispose();
      stop.dispose();
    });
  }
}

class _ModbusAdapter extends ModbusHost {
  _ModbusAdapter(this.s, this.run, this.prep);
  final AppSession s;
  final Future<void> Function(JsonMap, {JsonMap? overrides}) run;
  final ValueChanged<JsonMap> prep;
  @override
  JsonMap get request => s.draft;
  @override
  JsonMap get state => s.state;
  @override
  List<JsonMap> get events => s.events;
  @override
  bool get readOnly => s.readOnly;
  @override
  String get resultRunId => s.resultRunId;
  @override
  JsonMap? get resultRequest => s.resultRequest;
  @override
  JsonMap? get originalResultRequest => s.originalResultRequest;
  @override
  Stream<JsonMap> get eventStream => s.eventStream;
  @override
  Future<dynamic> command(JsonMap c) => s.command(c);
  @override
  void prepare(JsonMap r) => prep(r);
  @override
  Future<void> saveRequest(JsonMap r) => s.saveRequest(r);
  @override
  Future<void> collectionSaved(JsonMap state) async {
    s.stateChanged(state);
    await s.refreshSource();
  }

  @override
  Future<void> reviewAndRun(JsonMap r) => run(r);
  @override
  Future<void> exportText(String n, String t) => s.exportText(n, t);
  @override
  void started(JsonMap r) => s.started(r);
  @override
  void addListener(VoidCallback listener) => s.addListener(listener);
  @override
  void removeListener(VoidCallback listener) => s.removeListener(listener);
}
