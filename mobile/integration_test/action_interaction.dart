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
        await tester.ensureVisible(finder);
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
