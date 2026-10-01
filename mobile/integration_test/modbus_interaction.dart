import 'dart:async';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/session.dart';

/// Arm before the UI tap. A previous idle run is not evidence that the newly
/// confirmed operation has started or completed. Observe the app's own stream
/// so a fast response is retained even before the run reply updates its cache.
class ModbusOperationWait {
  ModbusOperationWait(this.session) : previousRunId = session.resultRunId {
    subscription = session.eventStream.listen(observed.add);
  }
  final AppSession session;
  final String previousRunId;
  final observed = <Map<String, dynamic>>[];
  late final StreamSubscription<Map<String, dynamic>> subscription;

  Future<String> wait(WidgetTester tester, {required String action}) =>
      waitForOperation(
        action: action,
        clock: tester.binding.clock.now,
        advance: tester.pump,
        diagnostics: () => find
            .descendant(of: find.byType(SnackBar), matching: find.byType(Text))
            .evaluate()
            .map((element) => (element.widget as Text).data ?? '')
            .join('；'),
      );

  Future<String> waitForOperation({
    required String action,
    required DateTime Function() clock,
    required Future<void> Function(Duration) advance,
    String Function()? diagnostics,
  }) async {
    final deadline = clock().add(const Duration(seconds: 30));
    while (true) {
      final run = session.resultRunId;
      if (run.isNotEmpty && run != previousRunId) {
        final events = [
          ...observed,
          ...session.events,
        ].where((event) => event['run_id'] == run).toList();
        final starts = events.where((event) => event['kind'] == 'started');
        final terminals = events.where((event) => event['kind'] == 'done');
        if (starts.isNotEmpty) {
          final data = starts.first['data'];
          final source = data is Map ? data['source'] : null;
          expect(
            source is Map ? source['action'] : null,
            action,
            reason: '新操作 $run 必须对应刚刚点击的 Modbus 动作',
          );
        }
        if (terminals.isNotEmpty) {
          final data = terminals.last['data'];
          final status = data is Map ? data['status'] : null;
          final error = data is Map ? data['error'] : null;
          if (status != 'completed') {
            throw TestFailure(
              'Modbus $action 操作 $run 结束状态 $status：${error ?? "无错误详情"}',
            );
          }
          if (starts.isNotEmpty) return run;
        }
      }
      if (clock().isAfter(deadline)) {
        final snackbars = diagnostics?.call() ?? '';
        throw TestFailure(
          '等待新 Modbus $action 操作的 started/done 超时；'
          '原操作 $previousRunId，当前 ${session.resultRunId}，'
          '状态 ${session.status}，界面错误 $snackbars',
        );
      }
      await advance(const Duration(milliseconds: 100));
    }
  }

  Future<void> dispose() => subscription.cancel();
}

class _ModbusScrollTarget {
  const _ModbusScrollTarget(this.state, this.start, this.delta);
  final ScrollableState state;
  final Offset start, delta;
}

/// Use the painted viewport, not an offscreen neighbour's content bounds.
/// Every gesture starts at a point that actually hits this scrollable. Clipping
/// ancestors, dialog routes and fixed bars can otherwise obscure its centre.
_ModbusScrollTarget? _visibleModbusScroll(
  WidgetTester tester, {
  required bool forward,
}) {
  final candidates = find
      .byWidgetPredicate(
        (widget) =>
            widget is Scrollable && widget.axisDirection == AxisDirection.down,
      )
      .evaluate()
      .toList()
      .reversed;
  for (final element in candidates) {
    if (ModalRoute.of(element)?.isCurrent == false) continue;
    if (element is! StatefulElement || element.state is! ScrollableState) {
      continue;
    }
    final state = element.state as ScrollableState;
    final position = state.position;
    final box = element.findRenderObject();
    if (box is! RenderBox ||
        !box.attached ||
        !box.hasSize ||
        !position.hasContentDimensions ||
        position.maxScrollExtent <= position.minScrollExtent) {
      continue;
    }
    final view = View.of(element);
    var viewport = MatrixUtils.transformRect(
      box.getTransformTo(null),
      Offset.zero & box.size,
    ).intersect(Offset.zero & (view.physicalSize / view.devicePixelRatio));
    RenderObject child = box;
    while (child.parent != null) {
      final parent = child.parent!;
      final clip = parent.describeApproximatePaintClip(child);
      if (clip != null) {
        viewport = viewport.intersect(
          MatrixUtils.transformRect(parent.getTransformTo(null), clip),
        );
      }
      child = parent;
    }
    if (viewport.isEmpty || viewport.width < 24 || viewport.height < 48) {
      continue;
    }
    for (final y in forward ? [.75, .5, .25] : [.25, .5, .75]) {
      for (final x in [.5, .25, .75]) {
        final point = Offset(
          viewport.left + viewport.width * x,
          viewport.top + viewport.height * y,
        );
        final hit = tester.hitTestOnBinding(point, viewId: view.viewId);
        if (!hit.path.any((entry) => identical(entry.target, box))) continue;
        final available = forward
            ? point.dy - viewport.top - 8
            : viewport.bottom - point.dy - 8;
        final distance = (viewport.height * .6)
            .clamp(0.0, 320.0)
            .clamp(0.0, available)
            .toDouble();
        if (distance < 24) continue;
        return _ModbusScrollTarget(
          state,
          point,
          Offset(0, forward ? -distance : distance),
        );
      }
    }
  }
  return null;
}

/// Keep the finder unindexed until it has a match: Finder.first/last throws
/// during evaluate() when a lazy list has not built the target yet.
Future<void> revealModbusFinder(WidgetTester tester, Finder finder) async {
  final deadline = DateTime.now().add(const Duration(seconds: 30));
  var reset = true, backwards = 0, forwards = 0;
  while (finder.evaluate().isEmpty) {
    if (DateTime.now().isAfter(deadline)) {
      throw TestFailure('未找到 Modbus 控件：$finder');
    }
    final target = _visibleModbusScroll(tester, forward: !reset);
    if (target != null) {
      if (reset &&
          (backwards >= 12 ||
              target.state.position.pixels <=
                  target.state.position.minScrollExtent + .5)) {
        reset = false;
        continue;
      }
      if (reset || forwards < 28) {
        if (reset) {
          backwards++;
        } else {
          forwards++;
        }
        await tester.dragFrom(target.start, target.delta);
        await tester.pump(const Duration(milliseconds: 120));
        continue;
      }
    }
    await tester.pump(const Duration(milliseconds: 100));
  }
}

/// A cancelled shared review closes above the Modbus form. Wait for both
/// routes and the form's asynchronous submit to finish before the next tap.
/// A visible label alone does not mean that its action is enabled yet.
Future<void> waitForModbusInteraction(
  WidgetTester tester,
  Finder finder,
) async {
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
