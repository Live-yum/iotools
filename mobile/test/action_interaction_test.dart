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
}
