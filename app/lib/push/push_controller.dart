import 'dart:async';

import '../pairing/gateway_store.dart';
import '../transport/rpc_client.dart';
import 'device_id.dart';
import 'notifications.dart';
import 'push_provider.dart';
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
    void Function(String sessionId)? onSessionTap,
  })  : _unifiedPush = unifiedPush ?? UnifiedPushProvider(),
        _deviceIdStore = deviceIdStore ?? DeviceIdStore(const FlutterSecureKv()),
        // ignore: prefer_initializing_formals — private field, public named param.
        _onSessionTap = onSessionTap;

  final UnifiedPushProvider _unifiedPush;
  final DeviceIdStore _deviceIdStore;
  final void Function(String sessionId)? _onSessionTap;

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

  /// Sets up local notifications, permission, tap routing, and starts UnifiedPush
  /// (defaulting to the embedded distributor when present).
  Future<void> init() async {
    _deviceId = await _deviceIdStore.getOrCreate();
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
    _registerIfPossible();
    _fetchVapidKey(client);
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
  }

  Future<void> dispose() async {
    await _tapSub?.cancel();
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
    _registerIfPossible();
  }

  void _emitTap(String sessionId) => _onSessionTap?.call(sessionId);

  Future<void> _registerIfPossible() async {
    final client = _client;
    final target = _target;
    if (client == null || target == null || _deviceId == null) return;
    try {
      await client.call('push.register', {'device_id': _deviceId, ...target.toParams()});
    } catch (_) {}
  }
}
