import 'package:flutter_test/flutter_test.dart';
import 'package:iotools_mobile/core/json.dart';

void main() {
  test('profile URLs are readable while secrets remain hidden', () {
    expect(
      displayEndpoint(r'${http}/echo', {'http': 'http://127.0.0.1:8080'}),
      'http://127.0.0.1:8080/echo',
    );
    expect(displayEndpoint(r'${unknown}/echo', {}), r'${unknown}/echo（待解析变量）');
    expect(displayEndpoint(r'${token}', {'token': 'sensitive'}), '••••');
    expect(
      displayEndpoint(r'${http}', {
        'http': 'https://user:password@host/a?token=private&x=%2F',
      }),
      'https://••••@host/a?token=••••&x=%2F',
    );
    expect(redactedEndpoint('tcp://127.0.0.1:502'), 'tcp://127.0.0.1:502');
    expect(
      redactedEndpoint('https://host/?%74oken=secret'),
      'https://host/?%74oken=••••',
    );
  });
}
