import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

/// Keep the finder unindexed until it has a match: Finder.first/last throws
/// during evaluate() when a lazy list has not built the target yet.
Future<void> revealModbusFinder(WidgetTester tester, Finder finder) async {
  if (finder.evaluate().isNotEmpty) return;
  final scrollables = find.byWidgetPredicate(
    (widget) =>
        widget is Scrollable && widget.axisDirection == AxisDirection.down,
  );
  if (scrollables.evaluate().isNotEmpty) {
    final scrollable = scrollables.last;
    await tester.drag(scrollable, const Offset(0, 3000));
    await tester.pump(const Duration(milliseconds: 300));
    for (var step = 0; step < 28 && finder.evaluate().isEmpty; step++) {
      if (scrollables.evaluate().isEmpty) break;
      await tester.drag(scrollable, const Offset(0, -320));
      await tester.pump(const Duration(milliseconds: 120));
    }
  }
  final deadline = DateTime.now().add(const Duration(seconds: 30));
  while (finder.evaluate().isEmpty) {
    if (DateTime.now().isAfter(deadline)) {
      throw TestFailure('未找到 Modbus 控件：$finder');
    }
    await tester.pump(const Duration(milliseconds: 100));
  }
}

/// A cancelled shared review closes above the Modbus form. Wait for both
/// routes and the form's asynchronous submit to finish before the next tap.
/// A visible label alone does not mean that its action is enabled yet.
Future<void> waitForModbusInteraction(WidgetTester tester, Finder finder) async {
  final deadline = DateTime.now().add(const Duration(seconds: 30));
  while (true) {
    await tester.pump();
    final hits = finder.hitTestable().evaluate();
    if (hits.isNotEmpty) {
      final element = hits.last;
      var enabled = ModalRoute.of(element)?.isCurrent ?? true;
      void check(Widget widget) {
        if (widget is ButtonStyleButton && widget.onPressed == null) {
          enabled = false;
        }
      }

      check(element.widget);
      element.visitAncestorElements((ancestor) {
        check(ancestor.widget);
        return enabled;
      });
      if (enabled) return;
    }
    if (DateTime.now().isAfter(deadline)) {
      throw TestFailure('Modbus 控件仍不可交互：$finder');
    }
    await tester.pump(const Duration(milliseconds: 50));
  }
}
