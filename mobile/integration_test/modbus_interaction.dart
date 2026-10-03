import 'dart:async';
import 'dart:convert';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/session.dart';

/// Arm before the UI tap. A previous idle run is not evidence that the newly
/// confirmed operation has started or completed. Observe the app's own stream
/// so a fast response is retained even before the run reply updates its cache.
class ModbusOperationWait {
  ModbusOperationWait(this.session) : previousRunId = session.resultRunId {
    subscription = session.eventStream.listen(_observe);
  }
  final AppSession session;
  final String previousRunId;
  final observed = <Map<String, dynamic>>[];
  final _observedSizes = <int>[];
  int _observedBytes = 0;
  late final StreamSubscription<Map<String, dynamic>> subscription;

  void _observe(Map<String, dynamic> event) {
    if (!const ['started', 'unit-probe', 'done'].contains(event['kind'])) return;
    final snapshot = _modbusEventEvidence(event);
    final bytes = utf8.encode(jsonEncode(snapshot)).length;
    observed.add(snapshot);
    _observedSizes.add(bytes);
    _observedBytes += bytes;
    // A scan has at most 32 probes plus started/done. Ignore bulky register
    // traffic and retain enough bounded snapshots for the newly armed run.
    while (observed.length > 64 || _observedBytes > 256 * 1024) {
      observed.removeAt(0);
      _observedBytes -= _observedSizes.removeAt(0);
    }
  }

  Future<String> wait(
    WidgetTester tester, {
    required String action,
    String expectedStatus = 'completed',
  }) =>
      waitForOperation(
        action: action,
        expectedStatus: expectedStatus,
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
    String expectedStatus = 'completed',
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
            reason: '新操作必须对应刚刚点击的 Modbus 动作；${evidence(run)}',
          );
        }
        if (terminals.isNotEmpty) {
          final data = terminals.last['data'];
          final status = data is Map ? data['status'] : null;
          final error = data is Map ? data['error'] : null;
          if (status != expectedStatus) {
            throw TestFailure(
              'Modbus $action 结束状态 $status（预期 $expectedStatus）：'
              '${boundedModbusEvidence(error ?? "无错误详情")}；${evidence(run)}',
            );
          }
          if (starts.isNotEmpty) return run;
        }
      }
      if (clock().isAfter(deadline)) {
        final snackbars = boundedModbusEvidence(diagnostics?.call() ?? '');
        throw TestFailure(
          '等待新 Modbus $action 操作的 started/done 超时；'
          '原操作 ${boundedModbusEvidence(previousRunId)}，'
          '界面错误 $snackbars；${evidence(session.resultRunId)}',
        );
      }
      await advance(const Duration(milliseconds: 100));
    }
  }

  String evidence(String runId) => modbusRunEvidence(
    session,
    runId: runId,
    observed: observed,
  );

  Future<void> dispose() => subscription.cancel();
}

String boundedModbusEvidence(Object? value, [int limit = 512]) {
  final text = '$value';
  return text.length <= limit ? text : '${text.substring(0, limit - 1)}…';
}

Map<String, dynamic> _modbusEventEvidence(Map<String, dynamic> event) {
  final raw = event['data'];
  final data = raw is Map ? raw : const {};
  final source = data['source'];
  Object? field(Object? value) => value == null || value is num || value is bool
      ? value
      : boundedModbusEvidence(value);
  return {
    'run_id': boundedModbusEvidence(event['run_id'], 128),
    'kind': boundedModbusEvidence(event['kind'], 32),
    'data': {
      if (source is Map)
        'source': {
          for (final key in ['action', 'endpoint', 'unit'])
            if (source.containsKey(key)) key: field(source[key]),
        },
      for (final key in [
        'unit', 'responsive', 'exception', 'type', 'address', 'count',
        'status', 'error',
      ])
        if (data.containsKey(key)) key: field(data[key]),
    },
  };
}

/// Only the active run's relevant events, bounded per event and overall. Keep
/// retained UI state separate from stream observations to expose missing data.
String modbusRunEvidence(
  AppSession session, {
  String? runId,
  Iterable<Map<String, dynamic>> observed = const [],
}) {
  final run = runId ?? session.resultRunId;
  String summarize(Iterable<Map<String, dynamic>> events) {
    final relevant = events.where((event) =>
        event['run_id'] == run &&
        const ['started', 'unit-probe', 'done'].contains(event['kind'])).toList();
    final tail = relevant.skip(relevant.length > 8 ? relevant.length - 8 : 0);
    return '[${tail.map((event) => boundedModbusEvidence(jsonEncode(_modbusEventEvidence(event)), 384)).join(', ')}]';
  }

  return boundedModbusEvidence(
    'run=${boundedModbusEvidence(run)} '
    'current=${boundedModbusEvidence(session.resultRunId)} '
    'status=${boundedModbusEvidence(session.status)} running=${session.running} '
    'error=${boundedModbusEvidence(session.error)} '
    'dropped=${session.dropped} evicted=${session.evicted} '
    'retained=${summarize(session.events)} observed=${summarize(observed)}',
    8192,
  );
}

/// Assert what the UI can actually render, never a stale run or stream-only
/// result. A completed scan can still contain an individual probe failure.
void expectModbusUnitProbe(
  AppSession session,
  String runId, {
  required int unit,
  required bool responsive,
  bool exception = false,
}) {
  final reason = modbusRunEvidence(session, runId: runId);
  expect(session.resultRunId, runId, reason: reason);
  final probes = session.events.where((event) {
    final data = event['data'];
    return event['run_id'] == runId &&
        event['kind'] == 'unit-probe' &&
        data is Map &&
        data['unit'] == unit;
  }).toList();
  expect(probes, hasLength(1), reason: '单元 $unit 必须保留一条探测结果；$reason');
  final data = probes.single['data'] as Map;
  expect(data['responsive'], responsive, reason: reason);
  expect(data['exception'] == true, exception, reason: reason);
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
