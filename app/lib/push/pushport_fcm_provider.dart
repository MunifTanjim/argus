import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:meta/meta.dart';

import '../pairing/gateway_store.dart';
import 'push_message.dart';
import 'push_provider.dart';
import 'pushport_client.dart';
import 'unifiedpush_background.dart' show decodeUnifiedPush;
import 'webpush_crypto.dart';

/// Persists and loads a [WebPushKeys] keypair.
abstract class WebPushKeyStore {
  /// Returns the persisted keypair, generating and persisting one on first use.
  Future<WebPushKeys> loadOrCreate();
}

/// Production store: persists the keypair in secure storage.
class SecureWebPushKeyStore implements WebPushKeyStore {
  SecureWebPushKeyStore([SecureKv? kv]) : _kv = kv ?? const FlutterSecureKv();

  final SecureKv _kv;

  static const _privKey = 'webpush_private_key';
  static const _authKey = 'webpush_auth';
  static const _pubKey = 'webpush_p256dh';

  @override
  Future<WebPushKeys> loadOrCreate() async {
    final priv = await _kv.read(_privKey);
    final auth = await _kv.read(_authKey);
    final pub = await _kv.read(_pubKey);
    if (priv != null && auth != null && pub != null) {
      final privBytes = base64Url
          .decode(priv + '=' * ((4 - priv.length % 4) % 4));
      return WebPushKeys(privateKey: privBytes, auth: auth, p256dh: pub);
    }
    final keys = await generateWebPushKeys();
    final privB64 = base64Url.encode(keys.privateKey).replaceAll('=', '');
    await _kv.write(_privKey, privB64);
    await _kv.write(_authKey, keys.auth);
    await _kv.write(_pubKey, keys.p256dh);
    return keys;
  }
}

@visibleForTesting
class FixedWebPushKeyStore implements WebPushKeyStore {
  const FixedWebPushKeyStore(this._keys);
  final WebPushKeys _keys;

  @override
  Future<WebPushKeys> loadOrCreate() async => _keys;
}

/// Delivers pushes over PushPort's FCM transport. The app generates its own
/// ECDH keypair and decrypts messages on-device — no Firebase types leak here;
/// FCM is consumed through the injected [getFcmToken] and [incomingEncrypted].
class PushPortFcmProvider implements PushProvider {
  PushPortFcmProvider({
    required this.client,
    required this.getFcmToken,
    required this.incomingEncrypted,
    Stream<List<int>>? incomingEncryptedOpens,
    WebPushKeyStore? keyStore,
  })  : _openStream = incomingEncryptedOpens,
        _keyStore = keyStore ?? SecureWebPushKeyStore();

  final PushPortClient client;
  final Future<String> Function() getFcmToken;
  final Stream<List<int>> incomingEncrypted;
  final Stream<List<int>>? _openStream;
  final WebPushKeyStore _keyStore;

  StreamSubscription<List<int>>? _sub;
  StreamSubscription<List<int>>? _openSub;

  @override
  String get name => 'pushport/fcm';

  @override
  Future<bool> isAvailable() async => true;

  @override
  Future<void> start({
    required void Function(PushTarget) onTarget,
    required void Function(PushMessage) onMessage,
    required void Function(PushMessage) onOpen,
  }) async {
    final keys = await _keyStore.loadOrCreate();
    final fcmToken = await getFcmToken();
    final sub = await client.subscribe(transport: 'fcm', token: fcmToken);
    onTarget(PushTarget(sub.endpoint, p256dh: keys.p256dh, auth: keys.auth));

    await _sub?.cancel();
    _sub = incomingEncrypted.listen((body) async {
      try {
        final plaintext = await decryptWebPush(
          privateKey: keys.privateKey,
          auth: keys.auth,
          body: body,
        );
        // Dedup and display both live in PushNotifications.show (the single
        // chokepoint, shared with the background isolate and UnifiedPush).
        onMessage(decodeUnifiedPush(Uint8List.fromList(plaintext)));
      } catch (_) {
        return;
      }
    });

    final opens = _openStream;
    if (opens != null) {
      await _openSub?.cancel();
      _openSub = opens.listen((body) async {
        try {
          final plaintext = await decryptWebPush(
            privateKey: keys.privateKey,
            auth: keys.auth,
            body: body,
          );
          onOpen(decodeUnifiedPush(Uint8List.fromList(plaintext)));
        } catch (_) {
          return;
        }
      });
    }
  }

  @override
  Future<void> refresh() async {}

  @override
  Future<void> stop() async {
    await _sub?.cancel();
    _sub = null;
    await _openSub?.cancel();
    _openSub = null;
  }
}
