import 'dart:async';

import 'package:unifiedpush/unifiedpush.dart' hide PushMessage;

import 'push_message.dart';
import 'push_provider.dart';
import 'unifiedpush_background.dart';

/// UnifiedPushProvider delivers via the UnifiedPush model: a distributor app on
/// the device hands the app an endpoint URL; the gateway POSTs (encrypted) Web
/// Push messages to it, and the distributor wakes the app to receive them. No
/// Google dependency — the default backend.
///
/// The UnifiedPush callbacks (endpoint + incoming message) are registered in
/// main() via [initUnifiedPush] so they also fire in the headless background
/// isolate when the app is killed. This provider only handles foreground concerns:
/// bridging endpoint updates to the controller (for gateway registration) and
/// selecting/registering a distributor. Message display lives in the background
/// handler.
class UnifiedPushProvider implements PushProvider {
  StreamSubscription<PushTarget>? _sub;

  /// The gateway's VAPID public key, required by the embedded FCM distributor
  /// (passed as applicationServerKey at registration). External distributors
  /// ignore it. Set by the controller once fetched from the gateway.
  String? vapidPubKey;

  /// The distributor to prefer when none has been chosen yet — the app's own
  /// embedded FCM distributor (its package id). Set by the controller.
  String? preferredDistributor;

  @override
  String get name => 'unifiedpush';

  @override
  Future<bool> isAvailable() async {
    final distributors = await UnifiedPush.getDistributors();
    return distributors.isNotEmpty;
  }

  /// Installed distributor apps detected on the device (package ids).
  Future<List<String>> availableDistributors() => UnifiedPush.getDistributors();

  /// The currently selected distributor, or null if none is chosen yet.
  Future<String?> savedDistributor() => UnifiedPush.getDistributor();

  /// Persists the user's distributor choice (used before [start]).
  Future<void> chooseDistributor(String distributor) =>
      UnifiedPush.saveDistributor(distributor);

  @override
  Future<void> start({
    required void Function(PushTarget) onTarget,
    required void Function(PushMessage) onMessage, // display handled in main()
    required void Function(PushMessage) onOpen, // taps arrive via local notifications
  }) async {
    // Forward endpoints (from main()'s onNewEndpoint) to the controller, replaying
    // the last one so registration isn't missed.
    await _sub?.cancel();
    _sub = unifiedPushEndpoints.listen(onTarget);
    final last = lastUnifiedPushEndpoint;
    if (last != null) onTarget(last);

    // Pick a distributor: the last acknowledged one, else the preferred embedded
    // distributor when installed, else the sole installed one. With several and no
    // preference/prior choice, wait for the user's pick.
    final saved = await UnifiedPush.getDistributor();
    final distributors = await UnifiedPush.getDistributors();
    final pick = saved ??
        (preferredDistributor != null &&
                distributors.contains(preferredDistributor)
            ? preferredDistributor
            : null) ??
        (distributors.length == 1 ? distributors.single : null);
    if (pick == null) return;

    await UnifiedPush.saveDistributor(pick);
    // The embedded distributor requires the VAPID key; if it's not available yet,
    // defer registration to reregister() once the controller fetches it on connect.
    if (pick == preferredDistributor && vapidPubKey == null) return;
    await register();
  }

  /// Registers with the saved distributor. The caller must have selected one
  /// (auto-picked in [start] or chosen via [chooseDistributor]). vapid is required
  /// by the embedded FCM distributor; external distributors (Sunup, ntfy) ignore it.
  Future<void> register() => UnifiedPush.register(vapid: vapidPubKey);

  /// Re-registers with the current [vapidPubKey] — used when the key arrives after
  /// registration (e.g. fetched on connect).
  Future<void> reregister() => register();

  /// Forces the distributor to re-emit the endpoint (via onNewEndpoint) and waits
  /// for it to arrive, so callers can register the fresh endpoint immediately.
  /// Skips the embedded distributor until its required VAPID key is known; returns
  /// without waiting if no endpoint arrives within the timeout.
  @override
  Future<void> refresh() async {
    if (preferredDistributor != null &&
        await savedDistributor() == preferredDistributor &&
        vapidPubKey == null) {
      return;
    }
    // Subscribe before register() so the resulting endpoint isn't missed.
    final next = unifiedPushEndpoints.first;
    try {
      await register();
      await next.timeout(const Duration(seconds: 10));
    } on TimeoutException {
      // Distributor produced no endpoint in time; the caller falls back to the
      // currently known target.
    }
  }

  @override
  Future<void> stop() async {
    await _sub?.cancel();
    _sub = null;
    await UnifiedPush.unregister();
  }
}
