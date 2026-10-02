import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/session.dart';

String _bounded(Object? value, int limit) {
  final text = value?.toString() ?? '';
  return text.length <= limit
      ? text
      : '${text.substring(0, limit)}…[truncated]';
}

/// Test-only metadata: never retain request bodies, headers, or codec keys.
class HTTPFixtureDiagnostics {
  final _clock = Stopwatch()..start();
  final _counts = <String, int>{
    'run_requested': 0,
    'accepted': 0,
    'body_read': 0,
    'close_started': 0,
    'response_closed': 0,
    'errors': 0,
  };
  final _stages = <JsonMap>[];
  int _omitted = 0;

  void record(String stage, {String? path, Object? error}) {
    _counts.update(stage, (count) => count + 1);
    if (_stages.length == 32) {
      _omitted++;
      return;
    }
    _stages.add({
      'stage': stage,
      'elapsed_ms': _clock.elapsedMilliseconds,
      if (path != null) 'path': _bounded(path, 64),
      if (error != null) 'error': _bounded(error, 1024),
    });
  }

  JsonMap snapshot() => {
    'elapsed_ms': _clock.elapsedMilliseconds,
    'counts': {..._counts},
    'stages': [
      for (final stage in _stages) {...stage},
    ],
    'omitted_stages': _omitted,
  };
}

/// Read retained events only: polling `events` again would drain the UI queue.
JsonMap retainedHTTPDiagnostics(AppSession session) {
  final events = session.events.where(
    (event) => event['run_id']?.toString() == session.resultRunId,
  );
  final terminal = events.where((event) => event['kind'] == 'done').lastOrNull;
  return {
    'status': _bounded(session.status, 64),
    'error': _bounded(session.error, 2048),
    'event_kinds': events
        .map((event) => _bounded(event['kind'], 64))
        .toSet()
        .take(16)
        .toList(),
    'terminal_event': terminal == null
        ? null
        : _bounded(exactEncode(terminal), 4096),
  };
}
