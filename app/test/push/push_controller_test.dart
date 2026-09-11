import 'dart:async';

import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/push/device_id.dart';
import 'package:argus/push/push_controller.dart';
import 'package:argus/push/push_message.dart';
import 'package:argus/push/push_provider.dart';
import 'package:argus/push/push_target_store.dart';
import 'package:argus/push/unifiedpush_provider.dart';
import 'package:argus/transport/gateway_client.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:flutter_test/flutter_test.dart';

class _MemKv implements SecureKv {
  final _m = <String, String>{};
  @override
  Future<String?> read(String key) async => _m[key];
  @override
  Future<void> write(String key, String value) async => _m[key] = value;
  @override
  Future<void> delete(String key) async => _m.remove(key);
}

class _FakeGatewayClient implements GatewayClient {
  final calls = <String>[];
  final params = <Map<String, dynamic>>[];
  final responses = <String, Object?>{};
  final throws = <String, Object>{};

  @override
  Future<Object?> call(String method, [Object? p]) async {
    calls.add(method);
    if (p is Map) params.add((p as Map).cast<String, dynamic>());
    if (throws.containsKey(method)) throw throws[method]!;
    return responses[method];
  }

  @override
  Stream<RpcMessage> get notifications => StreamController<RpcMessage>().stream;

  @override
  FutureOr<void> close() {}
}

class _FakeUnifiedPushProvider extends UnifiedPushProvider {
  bool forceFreshTargetCalled = false;

  @override
  Future<bool> isAvailable() async => false;
  @override
  Future<List<String>> availableDistributors() async => [];
  @override
  Future<String?> savedDistributor() async => null;
  @override
  Future<PushTarget?> forceFreshTarget() async {
    forceFreshTargetCalled = true;
    return null;
  }
}

class _FakePushProvider implements PushProvider {
  _FakePushProvider(this.name, this._endpoint);

  @override
  final String name;
  final String _endpoint;
  int startCount = 0;
  int stopCount = 0;

  @override
  Future<bool> isAvailable() async => true;

  @override
  Future<void> start({
    required void Function(PushTarget) onTarget,
    required void Function(PushMessage) onMessage,
    required void Function(PushMessage) onOpen,
  }) async {
    startCount++;
    onTarget(PushTarget(_endpoint));
  }

  @override
  Future<void> refresh() async {}

  @override
  Future<void> stop() async {
    stopCount++;
  }
}

PushController _makeController({
  required _FakeUnifiedPushProvider unifiedPush,
  required List<PushProvider> extraProviders,
  required _FakeGatewayClient client,
}) {
  final kv = _MemKv();
  final controller = PushController(
    unifiedPush: unifiedPush,
    extraProviders: extraProviders,
    deviceIdStore: DeviceIdStore(kv),
    targetStore: PushTargetStore(kv),
    providerKv: kv,
    testDeviceId: 'test-device-id',
  );
  controller.attach(client);
  return controller;
}

void main() {
  test('useProvider activates fake provider and registers on target', () async {
    final client = _FakeGatewayClient();
    const endpoint = 'https://ppdev.example/push/abc';
    final fakeProvider = _FakePushProvider('pushport/fcm', endpoint);
    final unifiedPush = _FakeUnifiedPushProvider();

    final controller = _makeController(
      unifiedPush: unifiedPush,
      extraProviders: [fakeProvider],
      client: client,
    );

    await controller.useProvider('pushport/fcm');
    await Future<void>.delayed(Duration.zero);

    expect(controller.activeBackend, 'pushport/fcm');
    expect(client.calls, contains('push.register'));

    final regCall = client.params.firstWhere((p) => p.containsKey('endpoint'));
    expect(regCall['endpoint'], endpoint);
    expect(regCall['device_id'], 'test-device-id');

    await controller.dispose();
  });

  test('reregister(force: true) does not drive UnifiedPush when FCM is active', () async {
    final client = _FakeGatewayClient();
    const endpoint = 'https://ppdev.example/push/abc';
    final fakeProvider = _FakePushProvider('pushport/fcm', endpoint);
    final unifiedPush = _FakeUnifiedPushProvider();

    final controller = _makeController(
      unifiedPush: unifiedPush,
      extraProviders: [fakeProvider],
      client: client,
    );

    await controller.useProvider('pushport/fcm');
    await Future<void>.delayed(Duration.zero);
    expect(controller.activeBackend, 'pushport/fcm');

    client.calls.clear();
    client.params.clear();

    final ok = await controller.reregister(force: true);
    await Future<void>.delayed(Duration.zero);

    // The UP force path must not fire for FCM (it restarts its own provider).
    expect(unifiedPush.forceFreshTargetCalled, isFalse);

    // The FCM provider must be restarted to mint a fresh subscription.
    expect(fakeProvider.stopCount, greaterThan(0));
    expect(fakeProvider.startCount, greaterThan(1));

    expect(ok, isTrue);
    expect(client.calls, contains('push.register'));
    final regCall = client.params.firstWhere((p) => p.containsKey('endpoint'));
    expect(regCall['endpoint'], endpoint);

    await controller.dispose();
  });

  test('persists the provider choice and restores it on a fresh controller', () async {
    final kv = _MemKv();

    final c1 = PushController(
      unifiedPush: _FakeUnifiedPushProvider(),
      extraProviders: [_FakePushProvider('pushport/fcm', 'https://pp.example/ep')],
      deviceIdStore: DeviceIdStore(_MemKv()),
      targetStore: PushTargetStore(_MemKv()),
      providerKv: kv,
      testDeviceId: 'd',
    );
    c1.attach(_FakeGatewayClient());
    await c1.useProvider('pushport/fcm');
    expect(await kv.read('push_provider'), 'pushport/fcm');
    await c1.dispose();

    final c2 = PushController(
      unifiedPush: _FakeUnifiedPushProvider(),
      extraProviders: [_FakePushProvider('pushport/fcm', 'https://pp.example/ep')],
      deviceIdStore: DeviceIdStore(_MemKv()),
      targetStore: PushTargetStore(_MemKv()),
      providerKv: kv,
      testDeviceId: 'd',
    );
    await c2.activateInitialProvider();
    expect(c2.activeBackend, 'pushport/fcm');
    await c2.dispose();
  });

  test('pushPortAvailable is true when server.info reports pushPortConfigured: true', () async {
    final client = _FakeGatewayClient();
    client.responses['server.info'] = {
      'version': 'x',
      'nodes': [],
      'pushPortConfigured': true,
    };
    final controller = PushController(
      unifiedPush: _FakeUnifiedPushProvider(),
      extraProviders: [],
      deviceIdStore: DeviceIdStore(_MemKv()),
      targetStore: PushTargetStore(_MemKv()),
      testDeviceId: 'test-device-id',
    );
    controller.attach(client);
    await controller.refreshServerInfo();
    expect(controller.pushPortAvailable, isTrue);
    await controller.dispose();
  });

  test('pushPortAvailable is false when server.info reports pushPortConfigured: false', () async {
    final client = _FakeGatewayClient();
    client.responses['server.info'] = {
      'version': 'x',
      'nodes': [],
      'pushPortConfigured': false,
    };
    final controller = PushController(
      unifiedPush: _FakeUnifiedPushProvider(),
      extraProviders: [],
      deviceIdStore: DeviceIdStore(_MemKv()),
      targetStore: PushTargetStore(_MemKv()),
      testDeviceId: 'test-device-id',
    );
    controller.attach(client);
    await controller.refreshServerInfo();
    expect(controller.pushPortAvailable, isFalse);
    await controller.dispose();
  });

  test('pushPortAvailable is false when server.info throws (fail-closed)', () async {
    final client = _FakeGatewayClient();
    client.throws['server.info'] = Exception('network error');
    final controller = PushController(
      unifiedPush: _FakeUnifiedPushProvider(),
      extraProviders: [],
      deviceIdStore: DeviceIdStore(_MemKv()),
      targetStore: PushTargetStore(_MemKv()),
      testDeviceId: 'test-device-id',
    );
    controller.attach(client);
    await controller.refreshServerInfo();
    expect(controller.pushPortAvailable, isFalse);
    await controller.dispose();
  });

}
