import 'package:flutter_test/flutter_test.dart';
import 'package:argus/push/pushport_config.dart';

void main() {
  test('exposes the build-time PushPort constants', () {
    expect(PushPortConfig.baseUrl, isNotEmpty);
    // appId is empty unless injected at build time via --dart-define=PUSHPORT_APP_ID=...
    expect(
      PushPortConfig.isConfigured,
      PushPortConfig.appId.isNotEmpty && PushPortConfig.baseUrl.isNotEmpty,
    );
  });
}
