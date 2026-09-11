import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:firebase_core/firebase_core.dart';
import 'package:firebase_messaging/firebase_messaging.dart';
import 'package:flutter/widgets.dart';

import 'notifications.dart';
import 'pushport_fcm_provider.dart' show SecureWebPushKeyStore;
import 'unifiedpush_background.dart' show decodeUnifiedPush;
import 'webpush_crypto.dart';

// PushPort packs the aes128gcm ciphertext under the FCM data key `e`, base64url
// unpadded (Go's base64.RawURLEncoding). See pushport internal/transport/fcm.go.
const _kCiphertextKey = 'e';

/// Decodes the PushPort ciphertext from an FCM data map; null when absent or malformed.
List<int>? decodeFcmBody(Map<String, dynamic> data) {
  final encoded = data[_kCiphertextKey];
  if (encoded is! String || encoded.isEmpty) return null;
  try {
    return base64Url.decode(encoded + '=' * ((4 - encoded.length % 4) % 4));
  } catch (_) {
    return null;
  }
}

final _controller = StreamController<List<int>>.broadcast();
final _openController = StreamController<List<int>>.broadcast();

/// Stream of raw aes128gcm-encrypted Web Push bodies from FCM foreground messages.
/// Feed this as [PushPortFcmProvider.incomingEncrypted].
Stream<List<int>> get fcmEncryptedMessages => _controller.stream;

/// Stream of encrypted bodies from FCM notification taps (onMessageOpenedApp).
/// Feed this as [PushPortFcmProvider.incomingEncryptedOpens] so the controller
/// routes the decrypted message to the deep-link/open path, not the display path.
Stream<List<int>> get fcmEncryptedOpens => _openController.stream;

/// Returns the FCM registration token for this device.
/// Pass as [PushPortFcmProvider.getFcmToken].
Future<String> fcmToken() async {
  final token = await FirebaseMessaging.instance.getToken();
  if (token == null) throw StateError('FCM token unavailable');
  return token;
}

List<int>? _extractBody(RemoteMessage message) => decodeFcmBody(message.data);

/// Call once after [Firebase.initializeApp] succeeds.
void initFcmListeners() {
  FirebaseMessaging.onMessage.listen((msg) {
    final body = _extractBody(msg);
    if (body != null) _controller.add(body);
  });
  // Tap on an already-shown notification: route to the open/deep-link path,
  // not the display path — the message was already shown by the background handler.
  FirebaseMessaging.onMessageOpenedApp.listen((msg) {
    final body = _extractBody(msg);
    if (body != null) _openController.add(body);
  });
}

/// Called by the Firebase plugin in a headless isolate when the app is killed;
/// shows the notification without launching the full UI.
///
/// Must be a top-level function; the @pragma keeps it from being tree-shaken so
/// the VM can locate it from native code.
@pragma('vm:entry-point')
Future<void> firebaseMessagingBackgroundHandler(RemoteMessage message) async {
  WidgetsFlutterBinding.ensureInitialized();
  await Firebase.initializeApp();

  final body = _extractBody(message);
  if (body == null) return;

  final keys = await SecureWebPushKeyStore().loadOrCreate();
  final List<int> plaintext;
  try {
    plaintext = await decryptWebPush(
      privateKey: keys.privateKey,
      auth: keys.auth,
      body: body,
    );
  } catch (_) {
    return;
  }

  // Dedup lives in PushNotifications.show (shared across the fg/bg isolates).
  await PushNotifications.instance.show(decodeUnifiedPush(Uint8List.fromList(plaintext)));
}
