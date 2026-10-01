import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

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
