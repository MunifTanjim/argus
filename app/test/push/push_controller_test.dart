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
import 'package:flutter/foundation.dart';
import 'package:flutter_test/flutter_test.dart';

import 'push_test_support.dart';

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
  String _endpoint;
  int startCount = 0;
  int stopCount = 0;
  int refreshCount = 0;

  Object? refreshError;

  @override
  PushLease lease = PushLease.none;

  void Function(PushTarget)? _onTarget;

  @override
  Future<bool> isAvailable() async => true;

  @override
  Future<void> start({
    required void Function(PushTarget) onTarget,
    required void Function(PushMessage) onMessage,
    required void Function(PushMessage) onOpen,
  }) async {
    startCount++;
    _onTarget = onTarget;
    onTarget(PushTarget(_endpoint));
  }

  /// Mints a new endpoint under a fresh lease, like the real PushPort providers.
  @override
  Future<void> refresh() async {
    refreshCount++;
    final error = refreshError;
    if (error != null) throw error;
    _endpoint = '$_endpoint/renewed$refreshCount';
    lease = PushLease.granted(unixIn(const Duration(hours: 24)));
    _onTarget?.call(PushTarget(_endpoint));
  }

  @override
  Future<void> stop() async {
    stopCount++;
  }
}

/// A lease inside its renewal lead: renewal is due, and the endpoint still
/// delivers until it expires.
PushLease _dueLease() =>
    PushLease(expiresAt: unixIn(const Duration(hours: 1)), renewAt: 1);

/// A lease that ran out: renewal is due and the endpoint is dead.
const _lapsedLease = PushLease(expiresAt: 1, renewAt: 1);

List<String> _registeredEndpoints(_FakeGatewayClient client) => [
      for (final p in client.params)
        if (p['endpoint'] != null) p['endpoint'] as String
    ];

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

/// Left unattached, so a test can time the attach itself.
PushController _controller({
  List<PushProvider> providers = const [],
  PushTargetStore? store,
}) =>
    PushController(
      unifiedPush: _FakeUnifiedPushProvider(),
      extraProviders: providers,
      deviceIdStore: DeviceIdStore(_MemKv()),
      targetStore: store ?? PushTargetStore(_MemKv()),
      providerKv: _MemKv(),
      testDeviceId: 'd',
    );

_FakeGatewayClient _pushPortClient() => _FakeGatewayClient()
  ..responses['server.info'] = {
    'version': 'x',
    'nodes': [],
    'pushPortConfigured': true,
  };

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

  test('a saved PushPort choice waits for gateway attach, then activates when available', () async {
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
    expect(c2.activeBackend, isNull);

    final client = _FakeGatewayClient();
    client.responses['server.info'] = {
      'version': 'x',
      'nodes': [],
      'pushPortConfigured': true,
    };
    c2.attach(client);
    await Future<void>.delayed(Duration.zero);
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
      providerKv: _MemKv(),
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

  test('iOS default PushPort provider does not activate at startup', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);

    final provider = _FakePushProvider('pushport/apns', 'https://pp.example/ep');
    final controller = PushController(
      unifiedPush: _FakeUnifiedPushProvider(),
      extraProviders: [provider],
      deviceIdStore: DeviceIdStore(_MemKv()),
      targetStore: PushTargetStore(_MemKv()),
      providerKv: _MemKv(),
      testDeviceId: 'd',
    );

    await controller.activateInitialProvider();
    expect(controller.activeBackend, isNull);
    expect(provider.startCount, 0);
    await controller.dispose();
  });

  test('PushPort provider activates and registers on attach when the gateway has PushPort', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);

    final client = _FakeGatewayClient();
    client.responses['server.info'] = {
      'version': 'x',
      'nodes': [],
      'pushPortConfigured': true,
    };
    final provider = _FakePushProvider('pushport/apns', 'https://pp.example/ep');
    final controller = PushController(
      unifiedPush: _FakeUnifiedPushProvider(),
      extraProviders: [provider],
      deviceIdStore: DeviceIdStore(_MemKv()),
      targetStore: PushTargetStore(_MemKv()),
      providerKv: _MemKv(),
      testDeviceId: 'd',
    );

    controller.attach(client);
    await Future<void>.delayed(Duration.zero);

    expect(controller.activeBackend, 'pushport/apns');
    expect(provider.startCount, 1);
    expect(client.calls, contains('push.register'));
    await controller.dispose();
  });

  test('a near-expiry PushPort endpoint is renewed on the next attach', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);

    final client = _pushPortClient();
    final provider = _FakePushProvider('pushport/apns', 'https://pp.example/ep');
    final controller = _controller(providers: [provider]);

    controller.attach(client);
    await Future<void>.delayed(Duration.zero);
    expect(provider.refreshCount, 0, reason: 'a fresh endpoint needs no renewal');
    expect(_registeredEndpoints(client), ['https://pp.example/ep']);

    // The endpoint crosses into the renew lead while the app keeps running.
    provider.lease = _dueLease();
    controller.attach(client);
    await Future<void>.delayed(Duration.zero);

    expect(provider.refreshCount, 1);
    expect(provider.startCount, 1, reason: 'already active, so no restart');
    expect(controller.target!.endpoint, 'https://pp.example/ep/renewed1');
    expect(_registeredEndpoints(client).last, 'https://pp.example/ep/renewed1',
        reason: 'the renewed endpoint must reach the gateway');

    controller.attach(client);
    await Future<void>.delayed(Duration.zero);

    expect(provider.refreshCount, 1,
        reason: 'the renewed lease is not due, so no second subscription');
    await controller.dispose();
  });

  test('a failed renewal still registers the endpoint the app already has',
      () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);

    final client = _pushPortClient();
    final provider = _FakePushProvider('pushport/apns', 'https://pp.example/ep');
    final controller = _controller(providers: [provider]);

    controller.attach(client);
    await Future<void>.delayed(Duration.zero);

    provider.lease = _dueLease();
    provider.refreshError = Exception('relay unreachable');
    controller.attach(client);
    await Future<void>.delayed(Duration.zero);

    expect(provider.refreshCount, 1);
    expect(controller.target!.endpoint, 'https://pp.example/ep');
    expect(_registeredEndpoints(client).last, 'https://pp.example/ep',
        reason: 'the old endpoint is still inside its lead, so register it');
    await controller.dispose();
  });

  test('a failed renewal past expiry drops the dead endpoint', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);

    final client = _pushPortClient();
    final store = PushTargetStore(_MemKv());
    final provider = _FakePushProvider('pushport/apns', 'https://pp.example/ep');
    final controller = _controller(providers: [provider], store: store);

    controller.attach(client);
    await Future<void>.delayed(Duration.zero);
    client.params.clear();

    provider.lease = _lapsedLease;
    provider.refreshError = Exception('relay unreachable');
    controller.attach(client);
    await Future<void>.delayed(Duration.zero);

    expect(controller.target, isNull);
    expect(await store.load(), isNull,
        reason: 'the dead target is cleared, not merely skipped');
    expect(_registeredEndpoints(client), isEmpty,
        reason: 'an expired endpoint must not be registered as if it worked');
    await controller.dispose();
  });

  test('a stored target whose lease lapsed while the app was closed is dropped',
      () async {
    final kv = _MemKv();
    final store = PushTargetStore(kv);
    await store.save(const PushTarget('https://pp.example/dead'),
        lease: _lapsedLease);

    final controller = _controller(store: store);
    await controller.loadStoredTarget();

    expect(controller.target, isNull);
    expect(await store.load(), isNull,
        reason: 'the dead target is cleared, not merely skipped');
    await controller.dispose();
  });

  test('a stored target with a live lease is restored', () async {
    final store = PushTargetStore(_MemKv());
    final lease = PushLease.granted(unixIn(const Duration(hours: 24)));
    await store.save(const PushTarget('https://pp.example/live'), lease: lease);

    final controller = _controller(store: store);
    await controller.loadStoredTarget();

    expect(controller.target, const PushTarget('https://pp.example/live'));
    await controller.dispose();
  });

  test('a stored target with no lease is restored', () async {
    final store = PushTargetStore(_MemKv());
    await store.save(const PushTarget('https://up.example/ep'));

    final controller = _controller(store: store);
    await controller.loadStoredTarget();

    expect(controller.target, const PushTarget('https://up.example/ep'));
    await controller.dispose();
  });

  test('the expiry is persisted with the target it was minted for', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);

    final client = _pushPortClient();
    final store = PushTargetStore(_MemKv());
    final provider = _FakePushProvider('pushport/apns', 'https://pp.example/ep');
    provider.lease = PushLease.granted(unixIn(const Duration(hours: 24)));
    final controller = _controller(providers: [provider], store: store);

    controller.attach(client);
    await Future<void>.delayed(Duration.zero);

    expect((await store.load())!.lease.expiresAt, provider.lease.expiresAt);
    await controller.dispose();
  });

  test('reregister without force asks the backend for a current endpoint',
      () async {
    final client = _FakeGatewayClient();
    final provider = _FakePushProvider('pushport/fcm', 'https://pp.example/ep');
    final controller = _makeController(
      unifiedPush: _FakeUnifiedPushProvider(),
      extraProviders: [provider],
      client: client,
    );

    await controller.useProvider('pushport/fcm');
    await Future<void>.delayed(Duration.zero);
    client.params.clear();

    final ok = await controller.reregister();
    await Future<void>.delayed(Duration.zero);

    expect(ok, isTrue);
    expect(provider.refreshCount, 1);
    expect(provider.stopCount, 0, reason: 'without force the provider keeps running');
    expect(_registeredEndpoints(client).last, 'https://pp.example/ep/renewed1');
    await controller.dispose();
  });

  test('PushPort provider does not activate on attach when the gateway lacks PushPort', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);

    final client = _FakeGatewayClient();
    client.responses['server.info'] = {
      'version': 'x',
      'nodes': [],
      'pushPortConfigured': false,
    };
    final provider = _FakePushProvider('pushport/apns', 'https://pp.example/ep');
    final controller = PushController(
      unifiedPush: _FakeUnifiedPushProvider(),
      extraProviders: [provider],
      deviceIdStore: DeviceIdStore(_MemKv()),
      targetStore: PushTargetStore(_MemKv()),
      providerKv: _MemKv(),
      testDeviceId: 'd',
    );

    controller.attach(client);
    await Future<void>.delayed(Duration.zero);

    expect(controller.activeBackend, isNull);
    expect(provider.startCount, 0);
    expect(client.calls, isNot(contains('push.register')));
    await controller.dispose();
  });

  test('a reconnect that loses PushPort turns the active provider off', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);

    final client = _FakeGatewayClient();
    client.responses['server.info'] = {
      'version': 'x',
      'nodes': [],
      'pushPortConfigured': true,
    };
    final provider = _FakePushProvider('pushport/apns', 'https://pp.example/ep');
    final controller = PushController(
      unifiedPush: _FakeUnifiedPushProvider(),
      extraProviders: [provider],
      deviceIdStore: DeviceIdStore(_MemKv()),
      targetStore: PushTargetStore(_MemKv()),
      providerKv: _MemKv(),
      testDeviceId: 'd',
    );

    controller.attach(client);
    await Future<void>.delayed(Duration.zero);
    expect(controller.activeBackend, 'pushport/apns');

    client.responses['server.info'] = {
      'version': 'x',
      'nodes': [],
      'pushPortConfigured': false,
    };
    controller.attach(client);
    await Future<void>.delayed(Duration.zero);

    expect(controller.activeBackend, isNull);
    expect(controller.target, isNull);
    expect(provider.stopCount, greaterThan(0));
    expect(client.calls, contains('push.unregister'));
    await controller.dispose();
  });

  test('a reconnect does not restart an already-active PushPort provider', () async {
    debugDefaultTargetPlatformOverride = TargetPlatform.iOS;
    addTearDown(() => debugDefaultTargetPlatformOverride = null);

    final client = _FakeGatewayClient();
    client.responses['server.info'] = {
      'version': 'x',
      'nodes': [],
      'pushPortConfigured': true,
    };
    final provider = _FakePushProvider('pushport/apns', 'https://pp.example/ep');
    final controller = PushController(
      unifiedPush: _FakeUnifiedPushProvider(),
      extraProviders: [provider],
      deviceIdStore: DeviceIdStore(_MemKv()),
      targetStore: PushTargetStore(_MemKv()),
      providerKv: _MemKv(),
      testDeviceId: 'd',
    );

    controller.attach(client);
    await Future<void>.delayed(Duration.zero);
    expect(provider.startCount, 1);

    client.calls.clear();
    controller.attach(client);
    await Future<void>.delayed(Duration.zero);

    expect(provider.startCount, 1);
    expect(client.calls, contains('push.register'));
    await controller.dispose();
  });
}
