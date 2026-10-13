import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import '../integration_test/app_test.dart' as workflow;
import '../integration_test/kafka_management_test.dart' as kafka;

void main() {
  testWidgets(
    'Kafka advanced panel is centered clear of an overlapping viewport edge',
    (tester) async {
      WidgetController.hitTestWarningShouldBeFatal = true;
      addTearDown(() => WidgetController.hitTestWarningShouldBeFatal = false);
      await tester.binding.setSurfaceSize(const Size(432, 752));
      addTearDown(() => tester.binding.setSurfaceSize(null));
      var expanded = false;
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Stack(
              children: [
                SingleChildScrollView(
                  child: Column(
                    children: [
                      const SizedBox(height: 1200),
                      ExpansionTile(
                        title: const Text('连接、安全与高级参数'),
                        onExpansionChanged: (value) => expanded = value,
                        children: const [Text('确认目标 Subject')],
                      ),
                      const SizedBox(height: 900),
                    ],
                  ),
                ),
                const Positioned(
                  top: 0,
                  left: 0,
                  right: 0,
                  height: 100,
                  child: ColoredBox(color: Colors.black),
                ),
              ],
            ),
          ),
        ),
      );
      final panel = find.text('连接、安全与高级参数');
      // The old ensureVisible alignment moves the title under the overlay.
      await tester.ensureVisible(panel);
      await tester.pump();
      expect(panel.hitTestable(), findsNothing);
      await kafka.tap(tester, panel);
      expect(expanded, isTrue);
      expect(panel.hitTestable(), findsOneWidget);
    },
  );
  testWidgets(
    'Kafka entry reopens the real IME connection and finishes editing',
    (tester) async {
      final controller = TextEditingController();
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: TextField(
              key: const ValueKey('param_confirm_subject'),
              controller: controller,
            ),
          ),
        ),
      );
      tester.testTextInput.unregister();
      try {
        await kafka.enter(tester, 'param_confirm_subject', 'fixture-schema');
        expect(controller.text, 'fixture-schema');
        await kafka.enter(tester, 'param_confirm_subject', 'second-schema');
        expect(controller.text, 'second-schema');
        final editable = tester.widget<EditableText>(find.byType(EditableText));
        expect(editable.focusNode.hasFocus, isFalse);
      } finally {
        tester.testTextInput.register();
        await tester.pumpWidget(const SizedBox());
        controller.dispose();
      }
    },
  );
  testWidgets(
    'workflow taps reveal controls below phone viewport before hit testing',
    (tester) async {
      WidgetController.hitTestWarningShouldBeFatal = true;
      addTearDown(() => WidgetController.hitTestWarningShouldBeFatal = false);
      await tester.binding.setSurfaceSize(const Size(480, 752));
      addTearDown(() => tester.binding.setSurfaceSize(null));
      var taps = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: SingleChildScrollView(
              child: Column(
                children: [
                  const SizedBox(height: 1200),
                  FilledButton(
                    key: const ValueKey('add_codec'),
                    onPressed: () {
                      taps++;
                    },
                    child: const Text('添加编解码器'),
                  ),
                  const SizedBox(height: 900),
                  FilledButton(
                    onPressed: () {
                      taps++;
                    },
                    child: const Text('第二个操作'),
                  ),
                ],
              ),
            ),
          ),
        ),
      );
      expect(
        tester.getCenter(find.byKey(const ValueKey('add_codec'))).dy,
        greaterThan(752),
      );
      await workflow.tapKey(tester, 'add_codec');
      expect(taps, 1);
      await workflow.tapText(tester, '第二个操作');
      expect(taps, 2);
    },
  );
  testWidgets(
    'Kafka request navigation distinguishes search text from request card',
    (tester) async {
      WidgetController.hitTestWarningShouldBeFatal = true;
      addTearDown(() => WidgetController.hitTestWarningShouldBeFatal = false);
      var opened = false;
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: Column(
              children: [
                const TextField(key: ValueKey('request_search')),
                InkWell(
                  onTap: () {
                    opened = true;
                  },
                  child: const SizedBox(
                    height: 120,
                    width: 400,
                    child: Text('Schema 注册'),
                  ),
                ),
              ],
            ),
          ),
        ),
      );
      await kafka.openRequest(tester, 'Schema 注册');
      expect(find.text('Schema 注册'), findsNWidgets(2));
      expect(opened, isTrue);
    },
  );
  testWidgets(
    'back taps the real route control then the request-list control',
    (tester) async {
      var returned = 0;
      await tester.pumpWidget(
        MaterialApp(
          home: Builder(
            builder: (context) => Scaffold(
              appBar: AppBar(
                leading: IconButton(
                  tooltip: '返回请求列表',
                  icon: const Icon(Icons.arrow_back),
                  onPressed: () {
                    returned++;
                  },
                ),
              ),
              body: FilledButton(
                onPressed: () {
                  Navigator.push(
                    context,
                    MaterialPageRoute<void>(
                      builder: (_) =>
                          Scaffold(appBar: AppBar(title: const Text('实体浏览'))),
                    ),
                  );
                },
                child: const Text('打开实体'),
              ),
            ),
          ),
        ),
      );
      await tester.tap(find.text('打开实体'));
      await tester.pumpAndSettle();
      final wrapper = find.byType(BackButton);
      expect(tester.widget<BackButton>(wrapper).onPressed, isNull);
      final actualBack = find
          .descendant(of: wrapper, matching: find.byType(IconButton))
          .hitTestable();
      expect(actualBack, findsOneWidget);
      expect(tester.widget<IconButton>(actualBack).onPressed, isNotNull);
      await kafka.back(tester);
      await tester.pumpAndSettle();
      expect(find.text('实体浏览'), findsNothing);
      expect(returned, 0);
      await kafka.back(tester);
      expect(returned, 1);
    },
  );
}
