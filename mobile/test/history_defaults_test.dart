import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/session.dart';
import 'support/fake_engine.dart';

class PreferencesPlatform extends FakePlatform {
  PreferencesPlatform(this.saved);
  final JsonMap saved;
  @override
  Future<Object?> invoke(String method, [JsonMap args = const {}]) async {
    if (method == 'settings.get') return {...saved, 'collection': 'iotools.yaml'};
    return super.invoke(method, args);
  }
}
class PreferencesEngine extends FakeEngine {
  bool? openedHistory;
  @override
  Future<JsonMap> open({bool readOnly = false, bool history = true, String? path}) async {
    openedHistory = history;
    return {...state, 'options': {'history': history, 'read_only': readOnly}};
  }
}
void main() {
  for (final saved in <JsonMap>[{}, {'history': false}, {'history': true}, {'history': 'invalid'}]) {
    test('session startup respects history preference $saved', () async {
      final engine = PreferencesEngine();
      final session = AppSession(engine, PreferencesPlatform(saved));
      await session.initialize();
      expect(engine.openedHistory, !saved.containsKey('history') || saved['history'] == true);
      expect(session.history, !saved.containsKey('history') || saved['history'] == true);
      session.dispose();
    });
  }
}
