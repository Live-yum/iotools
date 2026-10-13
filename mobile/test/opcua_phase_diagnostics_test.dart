import 'package:flutter_test/flutter_test.dart';
import '../integration_test/opcua_phase_diagnostics.dart';

void main() {
  test(
    'phase diagnostics emit only closed stage tokens and ordered integers',
    () {
      final messages = <String>[];
      final diagnostics = OPCPhaseDiagnostics(
        enabled: true,
        emit: messages.add,
      );
      for (final phase in OPCTestPhase.values) {
        diagnostics.mark(phase);
      }
      expect(messages.length, OPCTestPhase.values.length);
      var previousElapsed = 0;
      for (var i = 0; i < messages.length; i++) {
        final match = RegExp(
          r'^IOTOOLS_OPC_PHASE stage=([A-Za-z]+) sequence=([0-9]+) elapsed_ms=([0-9]+)$',
        ).firstMatch(messages[i]);
        expect(match, isNotNull);
        expect(match!.group(1), OPCTestPhase.values[i].name);
        expect(int.parse(match.group(2)!), i + 1);
        final elapsed = int.parse(match.group(3)!);
        expect(elapsed, greaterThanOrEqualTo(previousElapsed));
        previousElapsed = elapsed;
      }
    },
  );

  test('diagnostics disabled outside explicit fixture opt-in', () {
    final messages = <String>[];
    final diagnostics = OPCPhaseDiagnostics(enabled: false, emit: messages.add);
    for (final phase in OPCTestPhase.values) {
      diagnostics.mark(phase);
    }
    expect(messages, isEmpty);
    const enabled = bool.fromEnvironment('IOTOOLS_TEST_FIXTURES');
    OPCPhaseDiagnostics(emit: messages.add).mark(OPCTestPhase.testBodyStart);
    expect(messages.length, enabled ? 1 : 0);
  });

  test(
    'each test instance restarts its sequence without retaining app data',
    () {
      final messages = <String>[];
      for (var i = 0; i < 2; i++) {
        final diagnostics = OPCPhaseDiagnostics(
          enabled: true,
          emit: messages.add,
        );
        diagnostics.mark(OPCTestPhase.testBodyStart);
      }
      expect(messages.length, 2);
      expect(messages.every((line) => line.contains(' sequence=1 ')), isTrue);
    },
  );
}
