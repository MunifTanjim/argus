import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/push/apns_provider.dart';
import 'package:argus/push/device_id.dart';
import 'package:argus/push/push_controller.dart';
import 'package:argus/push/push_target_store.dart';
import 'package:argus/push/pushport_client.dart';
import 'package:argus/push/pushport_fcm_provider.dart';
import 'package:argus/push/webpush_crypto.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:http/testing.dart';

class _MemKv implements SecureKv {
  final _m = <String, String>{};
  @override
  Future<String?> read(String key) async => _m[key];
  @override
  Future<void> write(String key, String value) async => _m[key] = value;
  @override
  Future<void> delete(String key) async => _m.remove(key);
}

PushPortClient throwOnUseClient() => PushPortClient(
      baseUrl: 'https://unused.example',
      appId: 'unused',
      httpClient: MockClient((_) async => throw StateError('unused')),
    );

void main() {
  test('iOS default provider is pushport/apns when supplied', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);

    final apns = ApnsProvider(
      client: throwOnUseClient(),
      getApnsToken: () async => 'tok',
      writeKeys: ({required privB64url, required authB64url}) async => true,
    );
    final controller = PushController(
      extraProviders: [apns],
      testDeviceId: 'dev-1',
    );
    expect(controller.providerNames, contains('pushport/apns'));
    expect(controller.defaultProviderNameForTest, 'pushport/apns');
  });

  test('activateInitialProvider completes without throwing when default provider start fails', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);

    final kv = _MemKv();
    final keys = WebPushKeys(p256dh: 'PUB', auth: 'AUTH', privateKey: const [1, 2, 3]);
    final apns = ApnsProvider(
      client: throwOnUseClient(),
      getApnsToken: () async => throw StateError('APNs token unavailable (timeout)'),
      writeKeys: ({required privB64url, required authB64url}) async => true,
      keyStore: FixedWebPushKeyStore(keys),
    );
    final controller = PushController(
      extraProviders: [apns],
      deviceIdStore: DeviceIdStore(kv),
      targetStore: PushTargetStore(kv),
      providerKv: kv,
      testDeviceId: 'dev-1',
    );

    await expectLater(controller.activateInitialProvider(), completes);
    expect(controller.activeBackend, isNull);
  });
}
