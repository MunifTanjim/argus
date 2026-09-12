import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/push/apns_source.dart';

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();
  const channel = MethodChannel('dev.muniftanjim.argus/apns');

  setUp(() => setApnsChannelForTest(channel));

  test('apnsToken returns the native token', () async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'getApnsToken') return 'deadbeef';
      return null;
    });
    expect(await apnsToken(), 'deadbeef');
  });

  test('apnsToken throws when native returns null', () async {
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async => null);
    expect(apnsToken(), throwsStateError);
  });

  test('writeWebPushKeysToKeychain forwards args and returns result', () async {
    Map<Object?, Object?>? seen;
    TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .setMockMethodCallHandler(channel, (call) async {
      if (call.method == 'writeWebPushKeys') {
        seen = call.arguments as Map<Object?, Object?>;
        return true;
      }
      return null;
    });
    final ok = await writeWebPushKeysToKeychain(privB64url: 'p', authB64url: 'a');
    expect(ok, isTrue);
    expect(seen, {'priv': 'p', 'auth': 'a'});
  });

  test('apnsSessionTaps emits taps pushed from native', () async {
    final taps = <String>[];
    final sub = apnsSessionTaps.listen(taps.add);
    await TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger
        .handlePlatformMessage(
      channel.name,
      channel.codec.encodeMethodCall(const MethodCall('onTap', 'sess-1')),
      (_) {},
    );
    await Future<void>.delayed(Duration.zero);
    expect(taps, ['sess-1']);
    await sub.cancel();
  });
}
