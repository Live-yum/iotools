import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/json.dart';
import 'package:iotools_mobile/core/session.dart';
import 'support/fake_engine.dart';

class CollectionsEngine extends FakeEngine {
  String path = 'iotools.yaml';
  @override
  Future<JsonMap> open({
    bool readOnly = false,
    bool history = false,
    String? path,
  }) async => {...state, 'path': this.path};
  @override
  Future<Object?> command(JsonMap c) async {
    if (c['op'] == 'config.switch') {
      calls.add(c);
      if (c['confirmed'] == true) {
        path = c['path'];
        return {...state, 'path': path};
      }
      return {
        'token': 'bound',
        'collection': {'requests': []},
      };
    }
    if (c['op'] == 'config.get')
      return {'path': path, 'source': 'version: 1\n# $path\nrequests: []'};
    return super.command(c);
  }
}

void main() {
  test(
    'nested collection switching isolates identical request IDs and unsaved YAML in memory',
    () async {
      final e = CollectionsEngine(), p = FakePlatform(), s = AppSession(e, p);
      await s.initialize();
      s.prepare({'id': 'same', 'name': 'A draft', 'endpoint': 'a'});
      s.source += '\n# A unsaved';
      await s.switchCollection('folder/b.yaml', {'token': 'bound'});
      expect(s.drafts, isEmpty);
      expect(s.source, isNot(contains('A unsaved')));
      s.prepare({'id': 'same', 'name': 'B draft', 'endpoint': 'b'});
      await s.switchCollection('iotools.yaml', {'token': 'bound'});
      expect(s.drafts['same']!['name'], 'A draft');
      expect(s.source, contains('A unsaved'));
      expect(
        e.calls.where(
          (c) =>
              c['op'] == 'run' ||
              c['op'] == 'request.save' ||
              c['op'] == 'config.save',
        ),
        isEmpty,
      );
      expect(p.calls, contains('settings.save'));
      s.dispose();
    },
  );
}
