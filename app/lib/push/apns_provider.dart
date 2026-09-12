import 'dart:convert';

import 'package:flutter/foundation.dart' show debugPrint, kReleaseMode;

import 'push_message.dart';
import 'push_provider.dart';
import 'pushport_client.dart';
import 'pushport_fcm_provider.dart';
import 'pushport_renewal.dart';

/// Debug and profile builds are signed with `aps-environment: development`
/// (ios/Runner/Runner.entitlements), and APNs resolves those device tokens in the
/// sandbox alone.
const apnsUsesSandbox = !kReleaseMode;

/// Decryption, display and taps are native (Notification Service Extension and
/// AppDelegate), so [onMessage] and [onOpen] are unused on this path.
class ApnsProvider with PushPortRenewal implements PushProvider {
  ApnsProvider({
    required this.client,
    required this.getApnsToken,
    required this.writeKeys,
    this.sandbox = apnsUsesSandbox,
    WebPushKeyStore? keyStore,
  }) : _keyStore = keyStore ?? SecureWebPushKeyStore();

  final PushPortClient client;
  final bool sandbox;
  final Future<String> Function() getApnsToken;
  final Future<bool> Function({
    required String privB64url,
    required String authB64url,
  }) writeKeys;
  final WebPushKeyStore _keyStore;

  @override
  String get name => 'pushport/apns';

  @override
  Future<bool> isAvailable() async => true;

  @override
  Future<void> start({
    required void Function(PushTarget) onTarget,
    required void Function(PushMessage) onMessage,
    required void Function(PushMessage) onOpen,
  }) =>
      subscribe(onTarget);

  /// The APNs token rotates on restore and reinstall, and the extension can lose
  /// its keys, so every mint re-reads and re-writes both.
  @override
  Future<PushPortMint> mintSubscription() async {
    final keys = await _keyStore.loadOrCreate();
    final wroteKeys = await writeKeys(
      privB64url: base64Url.encode(keys.privateKey).replaceAll('=', ''),
      authB64url: keys.auth,
    );
    if (!wroteKeys) debugPrint('apns_provider: writeKeys failed — NSE cannot decrypt');
    final token = await getApnsToken();
    final sub = await client.subscribe(
      transport: 'apns',
      token: token,
      sandbox: sandbox,
    );
    return (
      sub: sub,
      target: PushTarget(sub.endpoint, p256dh: keys.p256dh, auth: keys.auth),
    );
  }

  @override
  Future<void> refresh() => renew();

  @override
  Future<void> stop() async => forgetLease();
}
