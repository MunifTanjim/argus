import 'dart:async';

import '../pairing/gateway_store.dart';
import '../transport/rpc_client.dart';
import 'device_id.dart';
import 'notifications.dart';
import 'push_provider.dart';
import 'push_target_store.dart';
import 'register.dart';
import 'unifiedpush_background.dart';
import 'unifiedpush_provider.dart';

/// The mobile app's Android applicationId — also the package of its embedded FCM
/// distributor, preferred by default when no distributor has been chosen.
const appPackageName = 'dev.muniftanjim.argus';

/// PushController coordinates push end to end over UnifiedPush / Web Push. It
/// activates a distributor (the embedded FCM one by default, or an external/chosen
/// one), shows incoming messages, tracks the device [target] and (re)registers it
/// on each connect — keyed by a stable device id so re-registration replaces the
/// prior endpoint — and surfaces tapped notifications as session ids.
class PushController {
  PushController({
    UnifiedPushProvider? unifiedPush,
    DeviceIdStore? deviceIdStore,
    PushTargetStore? targetStore,
    void Function(String sessionId)? onSessionTap,
  })  : _unifiedPush = unifiedPush ?? UnifiedPushProvider(),
        _deviceIdStore = deviceIdStore ?? DeviceIdStore(const FlutterSecureKv()),
        _targetStore = targetStore ?? PushTargetStore(const FlutterSecureKv()),
        // ignore: prefer_initializing_formals — private field, public named param.
        _onSessionTap = onSessionTap;

  final UnifiedPushProvider _unifiedPush;
  final DeviceIdStore _deviceIdStore;
  final PushTargetStore _targetStore;
  final void Function(String sessionId)? _onSessionTap;
  final _registrations = StreamController<bool>.broadcast();
  bool? _lastRegistration;
  PushTarget? _registeredTarget; // target already registered on this connection
  Future<bool>? _registerInFlight; // the running registration for _registeredTarget

  PushProvider? _active;
  PushTarget? _target;
  RpcClient? _client;
  String? _deviceId;
  String? _selectedDistributor;
  StreamSubscription<String>? _tapSub;

  PushTarget? get target => _target;

  /// The currently active backend's name, or null.
  String? get activeBackend => _active?.name;

  /// UnifiedPush registration failures (FailedReason name), for the UI to explain
  /// why no endpoint was produced.
  Stream<String> get pushFailures => unifiedPushFailures;

  /// Whether each gateway (re)registration attempt succeeded. Lets the UI surface
  /// a failed registration instead of silently leaving the device unreachable.
  Stream<bool> get registrations => _registrations.stream;

  /// The most recent registration result, or null if none has been attempted yet.
  /// The stream is broadcast (no replay), so a screen that opens after the attempt
  /// reads this to seed its state instead of waiting for the next event.
  bool? get lastRegistration => _lastRegistration;

  /// Sets up local notifications, permission, tap routing, and starts UnifiedPush
  /// (defaulting to the embedded distributor when present).
  Future<void> init() async {
    _deviceId = await _deviceIdStore.getOrCreate();
    // Restore the last known target so a relaunch can re-register it on connect
    // without waiting for the distributor to re-emit an endpoint.
    _target = await _targetStore.load();
    _unifiedPush.preferredDistributor = appPackageName;

    await PushNotifications.instance.init();
    await PushNotifications.instance.requestPermission();
    _tapSub = PushNotifications.instance.taps.listen(_emitTap);
    final launchId = await PushNotifications.instance.launchSessionId();
    if (launchId != null) _emitTap(launchId);

    if (await _unifiedPush.isAvailable()) await _activate(_unifiedPush);
  }

  /// Asks the gateway to send a test notification to this device's registered
  /// target. Throws if not connected or no target is registered yet.
  Future<void> sendTest() async {
    final client = _client;
    if (client == null) throw StateError('Not connected to the gateway');
    if (_target == null) {
      throw StateError('No push target yet — pick a distributor in settings');
    }
    await client.call('push.test', {'device_id': _deviceId});
  }

  /// Forces a fresh registration to recover from a stale/missing one (e.g. a
  /// failed test, or the gateway pruned a gone target). Re-requests an endpoint
  /// from the distributor and re-registers it with the gateway, bypassing the
  /// per-connection dedupe. Returns true once the gateway acknowledges.
  Future<bool> reregister() async {
    _registeredTarget = null; // bypass dedupe so the register actually fires
    _registerInFlight = null;
    // refresh() waits for the fresh endpoint, during which _setTarget may start
    // its own registration; _registerIfPossible then awaits that same in-flight
    // RPC rather than returning before it completes.
    await _active?.refresh();
    return _registerIfPossible();
  }

  /// Installed UnifiedPush distributor apps detected on the device.
  Future<List<String>> distributors() => _unifiedPush.availableDistributors();

  /// The selected UnifiedPush distributor: the acknowledged one if known, else the
  /// user's last choice (getDistributor only returns an acknowledged distributor,
  /// which is async for the embedded FCM one).
  Future<String?> currentDistributor() async =>
      (await _unifiedPush.savedDistributor()) ?? _selectedDistributor;

  /// Selects a distributor and (re)registers it as the active backend.
  Future<void> useDistributor(String distributor) async {
    _selectedDistributor = distributor;
    // The embedded FCM distributor needs the VAPID key at register() time.
    final client = _client;
    if (client != null && _unifiedPush.vapidPubKey == null) {
      await _fetchVapidKey(client);
    }
    await _unifiedPush.chooseDistributor(distributor);
    if (_active != _unifiedPush) await _active?.stop();
    await _activate(_unifiedPush);
    // start() auto-registers only a single/preferred distributor; for an explicit
    // pick alongside others, register the chosen one directly.
    await _unifiedPush.register();
  }

  /// Registers the current target once connected, and fetches the gateway's VAPID
  /// key (for the embedded FCM distributor).
  void attach(RpcClient client) {
    _client = client;
    _onAttached(client);
  }

  Future<void> _onAttached(RpcClient client) async {
    // New connection: register once (refreshing the gateway record), even if the
    // target is unchanged from the previous connection.
    _registeredTarget = null;
    _registerInFlight = null;
    // Fetch the VAPID key first (the embedded distributor needs it before it can
    // produce an endpoint), then register the known target. If we still have no
    // target, ask the backend to re-emit one so registration can follow.
    await _fetchVapidKey(client);
    await _registerIfPossible();
    if (_target == null) await _active?.refresh();
  }

  void detach() => _client = null;

  /// Unpair: drop the device's target server-side and stop the backend.
  Future<void> unregister() async {
    final client = _client;
    if (client != null && _deviceId != null) {
      try {
        await client.call('push.unregister', {'device_id': _deviceId});
      } catch (_) {}
    }
    await _active?.stop();
    _target = null;
    await _targetStore.clear();
  }

  Future<void> dispose() async {
    await _tapSub?.cancel();
    await _registrations.close();
  }

  Future<void> _fetchVapidKey(RpcClient client) async {
    String? key;
    try {
      final res = await client.call('push.vapidKey');
      key = (res is Map) ? res['key'] as String? : null;
    } catch (_) {
      return;
    }
    if (key == null || key.isEmpty) return;
    final changed = _unifiedPush.vapidPubKey != key;
    _unifiedPush.vapidPubKey = key;
    if (changed && _active == _unifiedPush) await _unifiedPush.reregister();
  }

  Future<void> _activate(PushProvider provider) async {
    _active = provider;
    await provider.start(
      onTarget: _setTarget,
      onMessage: PushNotifications.instance.show,
      onOpen: (m) {
        final id = m.sessionId;
        if (id != null) _emitTap(id);
      },
    );
  }

  void _setTarget(PushTarget t) {
    _target = t;
    _targetStore.save(t);
    _registerIfPossible();
  }

  void _emitTap(String sessionId) => _onSessionTap?.call(sessionId);

  Future<bool> _registerIfPossible() async {
    final client = _client;
    final target = _target;
    final deviceId = _deviceId;
    if (client == null || target == null || deviceId == null) return false;
    // Collapse the duplicate calls that fire on a single connect (the persisted
    // target and the distributor's re-emitted endpoint are normally identical).
    // Claim synchronously before awaiting so a concurrent call sees it and awaits
    // the same in-flight RPC instead of starting a second one or returning early.
    if (target == _registeredTarget) {
      return _registerInFlight ?? Future.value(true);
    }
    _registeredTarget = target;
    final fut = _register(client, deviceId, target);
    _registerInFlight = fut;
    return fut;
  }

  Future<bool> _register(RpcClient client, String deviceId, PushTarget target) async {
    final ok = await registerWithRetry(client, deviceId, target);
    if (!ok) _registeredTarget = null; // let a later attempt retry
    _lastRegistration = ok;
    _registrations.add(ok);
    return ok;
  }
}
