import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import '../integration_test/action_interaction.dart';

void main() {
  testWidgets(
    'real tap waits for current route and re-enabled parent after review',
    (tester) async {
      WidgetController.hitTestWarningShouldBeFatal = true;
      var hits = 0, enabled = false;
      late StateSetter update;
      await tester.pumpWidget(
        MaterialApp(
          home: StatefulBuilder(
            builder: (context, set) {
              update = set;
              return Scaffold(
                body: Column(
                  children: [
                    FilledButton(
                      key: const ValueKey('submit'),
                      onPressed: enabled ? () => hits++ : null,
                      child: const Text('submit'),
                    ),
                    TextButton(
                      onPressed: () => showDialog<void>(
                        context: context,
                        builder: (c) => AlertDialog(
                          actions: [
                            TextButton(
                              onPressed: () {
                                Navigator.pop(c);
                                Future<void>.delayed(
                                  const Duration(milliseconds: 250),
                                  () => update(() => enabled = true),
                                );
                              },
                              child: const Text('cancel'),
                            ),
                          ],
                        ),
                      ),
                      child: const Text('review'),
                    ),
                  ],
                ),
              );
            },
          ),
        ),
      );
      await tester.tap(find.text('review'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('cancel'));
      await tapReadyControl(tester, find.byKey(const ValueKey('submit')));
      expect(hits, 1);
      expect(find.text('cancel'), findsNothing);
    },
  );
  testWidgets(
    'lifecycle test driver follows strict listener graph and resumes after failure',
    (tester) async {
      resumeTestLifecycle(tester.binding);
      final states = <AppLifecycleState>[];
      final listener = AppLifecycleListener(
        binding: tester.binding,
        onStateChange: states.add,
      );
      try {
        pauseTestLifecycle(tester.binding);
        pauseTestLifecycle(tester.binding);
        expect(tester.binding.lifecycleState, AppLifecycleState.paused);
        resumeTestLifecycle(tester.binding);
        expect(states, [
          AppLifecycleState.inactive,
          AppLifecycleState.hidden,
          AppLifecycleState.paused,
          AppLifecycleState.hidden,
          AppLifecycleState.inactive,
          AppLifecycleState.resumed,
        ]);
        pauseTestLifecycle(tester.binding);
      } finally {
        resumeTestLifecycle(tester.binding);
        listener.dispose();
      }
      expect(tester.binding.lifecycleState, AppLifecycleState.resumed);
    },
  );
  testWidgets(
    'real IME style repeated editing reopens a closed connection by actual tap',
    (tester) async {
      final controller = TextEditingController();
      await tester.pumpWidget(
        MaterialApp(
          home: Scaffold(
            body: TextField(
              key: const ValueKey('number'),
              controller: controller,
            ),
          ),
        ),
      );
      final input = find.byKey(const ValueKey('number'));
      // IntegrationTestWidgetsFlutterBinding uses registerTestTextInput=false.
      tester.testTextInput.unregister();
      try {
        await enterReadyText(tester, input, '99');
        expect(controller.text, '99');
        await tester.enterText(input, '2147483648');
        await tester.pump();
        expect(
          controller.text,
          '99',
          reason: 'pinned SDK cached focus cannot reopen the closed IME client',
        );
        await enterReadyText(tester, input, '2147483648');
        expect(controller.text, '2147483648');
        await enterReadyText(tester, input, '18446744073709551615');
        expect(controller.text, '18446744073709551615');
      } finally {
        tester.testTextInput.register();
        await tester.pumpWidget(const SizedBox());
        controller.dispose();
      }
    },
  );
}
