import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/features/opcua/opcua_workspace.dart';
import '../../../integration_test/opcua_workflow_test.dart'
    show revealOpcuaAttribute;

const _attributeNames = [
  'NodeId',
  'NodeClass',
  'BrowseName',
  'DisplayName',
  'Description',
  'WriteMask',
  'UserWriteMask',
  'IsAbstract',
  'Symmetric',
  'InverseName',
  'ContainsNoLoops',
  'EventNotifier',
  'Value',
  'DataType',
  'ValueRank',
  'ArrayDimensions',
  'AccessLevel',
  'UserAccessLevel',
  'MinimumSamplingInterval',
  'Historizing',
  'Executable',
  'UserExecutable',
  'DataTypeDefinition',
  'RolePermissions',
  'UserRolePermissions',
  'AccessRestrictions',
  'AccessLevelEx',
];

class _AttributeFixture {
  final events = StreamController<UaMap>.broadcast(sync: true);
  final commands = <UaMap>[];
  final previews = <String, UaMap>{};
  final runs = <UaMap>[];
  final request = <String, dynamic>{
    'id': 'attributes',
    'protocol': 'opcua',
    'action': 'attributes',
    'endpoint': 'opc.tcp://127.0.0.1:48410',
    'params': {'node_id': 'ns=1;s=Temperature'},
  };
  final attributes = <UaMap>[
    for (var i = 0; i < _attributeNames.length; i++)
      {
        'attribute': _attributeNames[i],
        'attribute_id': i + 1,
        'value_type_name': _attributeNames[i] == 'Value' ? 'Int32' : 'String',
        'value': _attributeNames[i] == 'Value'
            ? 7
            : 'cached ${_attributeNames[i]}',
        'status': i == 9 ? 'BadAttributeIdInvalid' : 'Good',
        'source_timestamp': '2026-10-01T12:00:00Z',
        'server_timestamp': '2026-10-01T12:00:01Z',
      },
  ];
  bool includeLargeResult = false;
  int sequence = 0;

  Future<dynamic> command(UaMap command) async {
    commands.add(command);
    switch (command['op']) {
      case 'preview':
        final token = 'p${previews.length}',
            request = uaMap(command['request']);
        previews[token] = request;
        return {
          'token': token,
          'request': request,
          'mutates': request['action'] == 'write',
        };
      case 'run':
        final request = previews[command['token']]!;
        runs.add(request);
        final id = 'r${runs.length}', params = uaMap(request['params']);
        Future<void>.delayed(Duration.zero, () {
          void send(String kind, UaMap data) => events.add({
            'run_id': id,
            'seq': ++sequence,
            'kind': kind,
            'data': data,
          });
          if (request['action'] == 'attributes') {
            for (final attribute in attributes) {
              send('attribute', {'node_id': params['node_id'], ...attribute});
            }
            if (includeLargeResult) {
              send('attribute', {
                'truncated': true,
                'original_bytes': 20000,
                'result_id': 'full-attribute',
              });
            }
          }
          if (request['action'] == 'read') {
            send('value', {
              'node_id': params['node_id'],
              ...attributes.singleWhere(
                (row) => row['attribute'] == params['attribute'],
              ),
            });
          }
          send('done', {'status': 'completed'});
        });
        return {'run_id': id};
      case 'result.get':
        return {'data': 'complete cached result'};
      default:
        return {};
    }
  }
}

final _attributeCards = find.byWidgetPredicate(
  (widget) =>
      widget is Card &&
      widget.key is ValueKey<String> &&
      (widget.key! as ValueKey<String>).value.startsWith('ua-attribute-'),
);

Finder _card(String name) => find.byKey(ValueKey('ua-attribute-$name'));
Finder _value(String name) => find.byKey(ValueKey('ua-attribute-$name-value'));
Finder get _scrollable => find
    .descendant(
      of: find.byKey(const ValueKey('ua-scroll-attributes')),
      matching: find.byType(Scrollable),
    )
    .first;

Future<void> _tap(WidgetTester tester, Finder finder) async {
  await tester.ensureVisible(finder);
  await tester.pump();
  await tester.tap(finder);
  await tester.pumpAndSettle();
}

Future<void> _reveal(
  WidgetTester tester,
  Finder finder, {
  bool reverse = false,
}) async {
  // The large-text/short-screen layout has an outer scrollable for the header.
  await tester.ensureVisible(
    find.byKey(const ValueKey('ua-scroll-attributes')),
  );
  await tester.scrollUntilVisible(
    finder,
    reverse ? -300 : 300,
    scrollable: _scrollable,
    maxScrolls: 100,
  );
  await tester.pumpAndSettle();
}

Future<void> _open(
  WidgetTester tester,
  _AttributeFixture fixture, {
  double scale = 1,
}) async {
  await tester.pumpWidget(
    MaterialApp(
      builder: (context, child) => MediaQuery(
        data: MediaQuery.of(
          context,
        ).copyWith(textScaler: TextScaler.linear(scale)),
        child: child!,
      ),
      home: OpcuaWorkspace(
        request: fixture.request,
        command: fixture.command,
        events: fixture.events.stream,
        readOnly: false,
        onPrepare: (_) {},
      ),
    ),
  );
  expect(fixture.commands, isEmpty);
  await _tap(tester, find.byKey(const ValueKey('ua-tab-attributes')));
  expect(find.textContaining('全部 27 个标准属性'), findsOneWidget);
  final clock = Stopwatch()..start();
  await _tap(tester, find.byKey(const ValueKey('ua-refresh')));
  clock.stop();
  debugPrint(
    'OPC_ATTRIBUTE_INITIAL_RENDER '
    '${clock.elapsedMicroseconds}us cards=${_attributeCards.evaluate().length} '
    'selectables=${find.byType(SelectableText).evaluate().length}',
  );
}

void main() {
  for (final layout in [
    (name: 'narrow phone', size: const Size(390, 844), scale: 1.0),
    (name: 'desktop two panes', size: const Size(1200, 900), scale: 1.0),
    (name: 'large text short screen', size: const Size(390, 400), scale: 2.0),
  ]) {
    testWidgets(
      'all 27 attributes stay reachable with bounded mounts: ${layout.name}',
      (tester) async {
        await tester.binding.setSurfaceSize(layout.size);
        addTearDown(() => tester.binding.setSurfaceSize(null));
        final fixture = _AttributeFixture();
        final semantics = tester.ensureSemantics();
        try {
          await _open(tester, fixture, scale: layout.scale);
          expect(_attributeCards.evaluate().length, inInclusiveRange(1, 5));
          expect(
            find.byType(SelectableText).evaluate().length,
            lessThanOrEqualTo(31),
          );
          expect(_card('AccessLevelEx'), findsNothing);
          final beforeScroll = fixture.commands.length;
          var maxMounted = 0;
          for (final attribute in fixture.attributes) {
            final name = attribute['attribute'] as String;
            await _reveal(tester, _card(name));
            expect(_card(name), findsOneWidget);
            final values = tester
                .widgetList<SelectableText>(
                  find.descendant(
                    of: _card(name),
                    matching: find.byType(SelectableText),
                  ),
                )
                .map((widget) => widget.data)
                .toList();
            expect(values, [
              '${attribute['attribute_id']}',
              attribute['status'],
              attribute['value_type_name'],
              uaText(attribute['value']),
              attribute['source_timestamp'],
              attribute['server_timestamp'],
            ]);
            expect(
              tester.getSemantics(find.text(name)).label,
              startsWith('$name\n'),
            );
            final mounted = _attributeCards.evaluate().length;
            if (mounted > maxMounted) maxMounted = mounted;
            expect(mounted, lessThanOrEqualTo(5));
            expect(tester.takeException(), isNull);
          }
          expect(fixture.commands.length, beforeScroll);
          expect(_card('NodeId'), findsNothing);
          await _reveal(tester, _card('NodeId'), reverse: true);
          expect(
            tester.widget<SelectableText>(_value('NodeId')).data,
            'cached NodeId',
          );
          expect(fixture.commands.length, beforeScroll);
          debugPrint(
            'OPC_ATTRIBUTE_SCROLL ${layout.name}: '
            '27/27 reachable; maximum $maxMounted mounted',
          );
          await revealOpcuaAttribute(tester, 'ua-attribute-Value');
          expect(_card('Value'), findsOneWidget);
          await revealOpcuaAttribute(tester, 'ua-attribute-ValueRank');
          expect(_card('ValueRank'), findsOneWidget);
          await _tap(tester, find.byKey(const ValueKey('ua-refresh')));
          await revealOpcuaAttribute(
            tester,
            'ua-attribute-Value-value',
            fromStart: true,
          );
          expect(tester.widget<SelectableText>(_value('Value')).data, '7');
          expect(_attributeCards.evaluate().length, lessThanOrEqualTo(5));
          expect(fixture.runs.length, 2);
        } finally {
          semantics.dispose();
          await tester.pumpWidget(const SizedBox());
          await fixture.events.close();
        }
      },
    );
  }

  testWidgets(
    'remounted attributes preserve read targets, details, typed drafts and footer',
    (tester) async {
      await tester.binding.setSurfaceSize(const Size(390, 844));
      addTearDown(() => tester.binding.setSurfaceSize(null));
      final fixture = _AttributeFixture()..includeLargeResult = true;
      await _open(tester, fixture);
      await _reveal(tester, find.text('明确加载完整结果'));
      expect(find.textContaining('结果超出缓存边界'), findsOneWidget);
      await _reveal(tester, _card('Value'), reverse: true);
      final value = fixture.attributes.singleWhere(
        (row) => row['attribute'] == 'Value',
      );
      value['value'] = 99;
      await _tap(
        tester,
        find.descendant(of: _card('Value'), matching: find.text('读取此属性')),
      );
      expect(fixture.runs.last['action'], 'read');
      expect(uaMap(fixture.runs.last['params'])['attribute'], 'Value');
      expect(
        uaMap(fixture.runs.last['params'])['node_id'],
        'ns=1;s=Temperature',
      );
      expect(tester.widget<SelectableText>(_value('Value')).data, '99');
      expect(_attributeCards.evaluate().length, lessThanOrEqualTo(5));
      await _tap(
        tester,
        find.descendant(of: _card('Value'), matching: find.text('完整属性详情')),
      );
      expect(find.byType(AlertDialog), findsOneWidget);
      final detail = tester
          .widget<SelectableText>(
            find.descendant(
              of: find.byType(AlertDialog),
              matching: find.byType(SelectableText),
            ),
          )
          .data!;
      expect(jsonDecode(detail), {'node_id': 'ns=1;s=Temperature', ...value});
      await _tap(tester, find.text('关闭'));
      await _tap(
        tester,
        find.descendant(of: _card('Value'), matching: find.text('编辑值')),
      );
      final input = find.byKey(const ValueKey('ua-write-value-Int32'));
      await tester.enterText(input, '123');
      await _tap(tester, find.text('取消'));
      final beforeScroll = fixture.commands.length;
      await _reveal(tester, _card('AccessLevelEx'));
      expect(_card('Value'), findsNothing);
      await _reveal(tester, _card('Value'), reverse: true);
      expect(tester.widget<SelectableText>(_value('Value')).data, '99');
      await _tap(
        tester,
        find.descendant(of: _card('Value'), matching: find.text('编辑值')),
      );
      final editable = find.descendant(
        of: input,
        matching: find.byType(EditableText),
      );
      expect(tester.widget<EditableText>(editable).controller.text, '123');
      expect(fixture.commands.length, beforeScroll);
      await _tap(tester, find.byKey(const ValueKey('ua-preview-write')));
      expect(fixture.previews.values.last['action'], 'write');
      expect(
        uaMap(fixture.previews.values.last['params'])['attribute'],
        'Value',
      );
      expect(uaMap(fixture.previews.values.last['params'])['value'], '123');
      await _tap(tester, find.byKey(const ValueKey('ua-review-cancel')));
      expect(
        fixture.runs.where((request) => request['action'] == 'write'),
        isEmpty,
      );
      await _tap(tester, find.text('取消'));
      await _reveal(tester, find.text('明确加载完整结果'));
      await _tap(tester, find.text('明确加载完整结果'));
      expect(fixture.commands.last, {
        'op': 'result.get',
        'result_id': 'full-attribute',
      });
      expect(find.text('complete cached result'), findsOneWidget);
      await _tap(tester, find.text('关闭'));
      expect(tester.takeException(), isNull);
      await tester.pumpWidget(const SizedBox());
      await fixture.events.close();
    },
  );
}
