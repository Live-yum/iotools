part of 'opcua_workspace.dart';

// Dialog flows share the workspace lifecycle and existing controller.
extension _OpcuaWorkspaceDialogs on _OpcuaWorkspaceState {
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
        if (started && mounted) _selectSection('subscriptions');
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

}
