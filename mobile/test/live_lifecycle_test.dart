import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import '../integration_test/action_interaction.dart';

void main() {
  final binding = LiveTestWidgetsFlutterBinding.ensureInitialized();
  testWidgets(
    'read-only background acknowledgement completes without a paused frame',
    (tester) async {
      resumeTestLifecycle(binding);
      await tester.pumpWidget(const MaterialApp(home: Text('probe')));
      pauseTestLifecycle(binding);
      var stopped = false;
      Timer(const Duration(milliseconds: 80), () => stopped = true);
      await waitForBackgroundCondition(tester, () async => stopped);
      expect(binding.lifecycleState, AppLifecycleState.paused);
      resumeTestLifecycle(binding);
      await tester.pump();
      expect(stopped, isTrue);
    },
    timeout: const Timeout(Duration(seconds: 8)),
  );
}
