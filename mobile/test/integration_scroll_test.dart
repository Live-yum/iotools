import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import '../integration_test/app_test.dart' as workflow;
import '../integration_test/kafka_management_test.dart' as kafka;

void main() {
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
      await kafka.back(tester);
      await tester.pumpAndSettle();
      expect(find.text('实体浏览'), findsNothing);
      expect(returned, 0);
      await kafka.back(tester);
      expect(returned, 1);
    },
  );
}
