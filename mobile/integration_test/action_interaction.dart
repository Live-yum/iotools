import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// A dismissed review can leave its parent control present but disabled.
Future<void> tapReadyControl(WidgetTester tester, Finder finder) async {
  final end = DateTime.now().add(const Duration(seconds: 20));
  while (true) {
    if (finder.evaluate().length == 1) {
      final element = finder.evaluate().single;
      final route = ModalRoute.of(element);
      final widget = element.widget;
      final enabled = widget is ButtonStyleButton
          ? widget.enabled
          : widget is IconButton
          ? widget.onPressed != null
          : true;
      if ((route == null || route.isCurrent) && enabled) {
        await Scrollable.ensureVisible(element, alignment: .5);
        await tester.pump();
        if (finder.hitTestable().evaluate().length == 1) {
          await tester.tap(finder);
          await tester.pump();
          return;
        }
      }
    }
    if (DateTime.now().isAfter(end)) {
      throw TestFailure(
        'Control was not visible, enabled and on the current route: $finder',
      );
    }
    await tester.pump(const Duration(milliseconds: 50));
  }
}

/// Match Flutter's platform lifecycle transition graph; direct resumed→paused
/// bypasses the hidden/inactive transitions and fails real framework listeners.
void pauseTestLifecycle(WidgetsBinding binding) {
  if (binding.lifecycleState == null ||
      binding.lifecycleState == AppLifecycleState.detached) {
    binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
  }
  if (binding.lifecycleState == AppLifecycleState.resumed) {
    binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
  }
  if (binding.lifecycleState == AppLifecycleState.inactive) {
    binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
  }
  if (binding.lifecycleState == AppLifecycleState.hidden) {
    binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
  }
}

void resumeTestLifecycle(WidgetsBinding binding) {
  if (binding.lifecycleState == AppLifecycleState.paused) {
    binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
  }
  if (binding.lifecycleState == AppLifecycleState.hidden) {
    binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
  }
  if (binding.lifecycleState != AppLifecycleState.resumed) {
    binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
  }
}

/// Real integration bindings do not register TestTextInput's clear-client hook.
/// Re-tap the visible field after IME Done or a nested dialog so focus opens a
/// live input connection, rather than relying on showKeyboard's cached state.
Future<void> enterReadyText(
  WidgetTester tester,
  Finder finder,
  String value,
) async {
  await tapReadyControl(tester, finder);
  await tester.enterText(finder, value);
  await tester.pump();
  final editable = find.descendant(
    of: finder,
    matching: find.byType(EditableText),
    matchRoot: true,
  );
  expect(
    tester.widget<EditableText>(editable).controller.text,
    value,
    reason: '真实编辑控件必须收到完整精确输入',
  );
  await tester.testTextInput.receiveAction(TextInputAction.done);
  await tester.pump();
}

/// Paused live bindings do not produce frames. Poll only the read-only native
/// acknowledgement while backgrounded, then resume before requesting a frame.
Future<void> waitForBackgroundCondition(
  WidgetTester tester,
  Future<bool> Function() condition, {
  Duration timeout = const Duration(seconds: 15),
}) async {
  await tester.runAsync(() async {
    final deadline = DateTime.now().add(timeout);
    while (!await condition().timeout(timeout)) {
      if (DateTime.now().isAfter(deadline)) {
        throw TestFailure('后台内核未在时限内确认停止状态');
      }
      await Future<void>.delayed(const Duration(milliseconds: 25));
    }
  });
}
