import 'dart:async';

import 'package:meta/meta.dart';

import '../pairing/gateway_store.dart';
import '../transport/gateway_client.dart';
import 'device_id.dart';
import 'fcm_source.dart';
import 'notifications.dart';
import 'push_provider.dart';
import 'push_target_store.dart';
import 'pushport_client.dart';
import 'pushport_config.dart';
import 'pushport_fcm_provider.dart';
import 'register.dart';
import 'unifiedpush_background.dart';
import 'unifiedpush_provider.dart';

/// The mobile app's Android applicationId — also the package of its embedded FCM
/// distributor, preferred by default when no distributor has been chosen.
const appPackageName = 'dev.muniftanjim.argus';

/// Coordinates push end to end across backends: UnifiedPush (always) and the
/// PushPort/FCM provider (when [PushPortConfig.isConfigured]). Keeps [_active] as
/// the single running provider, (re)registers the device [target] on each
/// connect, and surfaces tapped notifications as session ids.
class PushController {
  PushController({
    UnifiedPushProvider? unifiedPush,
    @visibleForTesting
    List<PushProvider>? extraProviders,
    DeviceIdStore? deviceIdStore,
    PushTargetStore? targetStore,
    SecureKv? providerKv,
    void Function(String sessionId)? onSessionTap,
    @visibleForTesting
    String? testDeviceId,
  })  : _unifiedPush = unifiedPush ?? UnifiedPushProvider(),
        _deviceIdStore = deviceIdStore ?? DeviceIdStore(const FlutterSecureKv()),
        _targetStore = targetStore ?? PushTargetStore(const FlutterSecureKv()),
        _providerKv = providerKv ?? const FlutterSecureKv(),
        // ignore: prefer_initializing_formals — private field, public named param.
        _onSessionTap = onSessionTap {
    _deviceId = testDeviceId;
    _buildProviders(extraProviders);
  }

  final UnifiedPushProvider _unifiedPush;
  final DeviceIdStore _deviceIdStore;
  final PushTargetStore _targetStore;
  final SecureKv _providerKv;
  static const _kProviderKey = 'push_provider';
  final void Function(String sessionId)? _onSessionTap;
  final _registrations = StreamController<bool>.broadcast();
  late final Map<String, PushProvider> _providers;
  bool? _lastRegistration;
  PushTarget? _registeredTarget; // target already registered on this connection
  Future<bool>? _registerInFlight; // the running registration for _registeredTarget

  PushProvider? _active;
  PushTarget? _target;
  GatewayClient? _client;
  String? _deviceId;
  String? _selectedDistributor;
  StreamSubscription<String>? _tapSub;
  bool _pushPortAvailable = false;

  void _buildProviders(List<PushProvider>? extra) {
    final all = <PushProvider>[_unifiedPush];
    if (extra != null) {
      all.addAll(extra);
    } else if (PushPortConfig.isConfigured) {
      final ppClient = PushPortClient(
        baseUrl: PushPortConfig.baseUrl,
        appId: PushPortConfig.appId,
      );
      all.add(PushPortFcmProvider(
        client: ppClient,
        getFcmToken: fcmToken,
        incomingEncrypted: fcmEncryptedMessages,
        incomingEncryptedOpens: fcmEncryptedOpens,
      ));
    }
    _providers = {for (final p in all) p.name: p};
  }

  PushTarget? get target => _target;

  /// The currently active backend's name, or null.
  String? get activeBackend => _active?.name;

  /// UnifiedPush registration failures (FailedReason name), for the UI to explain
  /// why no endpoint was produced.
  Stream<String> get pushFailures => unifiedPushFailures;

  /// Whether each gateway (re)registration attempt succeeded.
  Stream<bool> get registrations => _registrations.stream;

  /// The most recent registration result, or null if none has been attempted yet.
  bool? get lastRegistration => _lastRegistration;

  /// Whether the connected gateway has a PushPort token configured.
  bool get pushPortAvailable => _pushPortAvailable;

  /// Sets up local notifications, permission, tap routing, and starts UnifiedPush
  /// (defaulting to the embedded distributor when present).
  Future<void> init() async {
    _deviceId ??= await _deviceIdStore.getOrCreate();
    _target = await _targetStore.load();
    _unifiedPush.preferredDistributor = appPackageName;

    await PushNotifications.instance.init();
    await PushNotifications.instance.requestPermission();
    _tapSub = PushNotifications.instance.taps.listen(_emitTap);
    final launchId = await PushNotifications.instance.launchSessionId();
    if (launchId != null) _emitTap(launchId);

    await activateInitialProvider();
  }

  /// Activates the persisted provider choice, falling back to UnifiedPush.
  /// Called from [init] on startup so a prior selection survives a restart.
  @visibleForTesting
  Future<void> activateInitialProvider() async {
    final saved = await _providerKv.read(_kProviderKey);
    final savedProvider = saved != null ? _providers[saved] : null;
    if (savedProvider != null) {
      await _activate(savedProvider);
    } else if (await _unifiedPush.isAvailable()) {
      await _activate(_unifiedPush);
    }
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

  /// Forces a fresh registration to recover from a stale/missing one. Pass
  /// [force] when the endpoint is permanently gone.
  Future<bool> reregister({bool force = false}) async {
    _registeredTarget = null;
    _registerInFlight = null;
    if (force) {
      await _targetStore.clear();
      _target = null;
      if (_active == _unifiedPush) {
        // UP: force-unregister first so the dead endpoint can't be re-emitted.
        final fresh = await _unifiedPush.forceFreshTarget();
        if (fresh == null) return false;
        _target = fresh;
        await _targetStore.save(fresh);
      } else {
        // Non-UP providers (pushport/fcm): restart to mint a fresh subscription.
        final provider = _active;
        if (provider != null) {
          await provider.stop();
          await _activate(provider);
        }
      }
    } else {
      await _active?.refresh();
    }
    return _registerIfPossible();
  }

  /// Installed UnifiedPush distributor apps detected on the device.
  Future<List<String>> distributors() => _unifiedPush.availableDistributors();

  /// The selected UnifiedPush distributor.
  Future<String?> currentDistributor() async =>
      (await _unifiedPush.savedDistributor()) ?? _selectedDistributor;

  /// Selects a UnifiedPush distributor and (re)registers it as the active backend.
  Future<void> useDistributor(String distributor) async {
    _selectedDistributor = distributor;
    final client = _client;
    if (client != null && _unifiedPush.vapidPubKey == null) {
      await _fetchVapidKey(client);
    }
    await _unifiedPush.chooseDistributor(distributor);
    if (_active != _unifiedPush) await _active?.stop();
    await _activate(_unifiedPush);
    await _unifiedPush.register();
  }

  /// Switches to the named provider, stopping the current one. Throws if the
  /// name is not among the available providers.
  Future<void> useProvider(String name) async {
    final provider = _providers[name];
    if (provider == null) throw ArgumentError('Unknown provider: $name');
    if (_active == provider) return;
    await _active?.stop();
    await _activate(provider);
    await _providerKv.write(_kProviderKey, name);
  }

  /// Registers the current target once connected, and fetches the gateway's VAPID
  /// key (for the embedded FCM distributor).
  void attach(GatewayClient client) {
    _client = client;
    _onAttached(client);
  }

  Future<void> _onAttached(GatewayClient client) async {
    _registeredTarget = null;
    _registerInFlight = null;
    await Future.wait([_fetchVapidKey(client), refreshServerInfo()]);
    await _registerIfPossible();
    if (_target == null) await _active?.refresh();
  }

  /// Tell the currently-connected gateway to stop pushing to this device, then
  /// detach.
  Future<void> unregisterFromCurrentGateway() async {
    final client = _client;
    final deviceId = _deviceId;
    _client = null;
    _registeredTarget = null;
    _registerInFlight = null;
    if (client == null || deviceId == null) return;
    await unregisterFromGateway(client, deviceId);
  }

  Future<void> dispose() async {
    await _tapSub?.cancel();
    await _registrations.close();
  }

  /// Fetches `server.info` to update [pushPortAvailable]. Fail-closed: if the
  /// call throws, [pushPortAvailable] is set to false.
  Future<void> refreshServerInfo() async {
    final client = _client;
    if (client == null) return;
    try {
      final res = await client.call('server.info');
      _pushPortAvailable = (res is Map) && res['pushPortConfigured'] == true;
    } catch (_) {
      _pushPortAvailable = false;
    }
  }

  Future<void> _fetchVapidKey(GatewayClient client) async {
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
    if (target == _registeredTarget) {
      return _registerInFlight ?? Future.value(true);
    }
    _registeredTarget = target;
    final fut = _register(client, deviceId, target);
    _registerInFlight = fut;
    return fut;
  }

  Future<bool> _register(GatewayClient client, String deviceId, PushTarget target) async {
    final ok = await registerWithRetry(client, deviceId, target);
    if (!ok) _registeredTarget = null;
    _lastRegistration = ok;
    _registrations.add(ok);
    return ok;
  }
}
