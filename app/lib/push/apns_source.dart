import 'dart:async';

import 'package:flutter/services.dart';
import 'package:meta/meta.dart';

MethodChannel _channel = const MethodChannel('dev.muniftanjim.argus/apns');

MethodChannel get apnsChannel => _channel;

@visibleForTesting
void setApnsChannelForTest(MethodChannel channel) {
  _channel = channel;
  _wired = false;
  _wireHandler();
}

final _taps = StreamController<String>.broadcast();
bool _wired = false;

Stream<String> get apnsSessionTaps {
  _wireHandler();
  return _taps.stream;
}

void _wireHandler() {
  if (_wired) return;
  _wired = true;
  _channel.setMethodCallHandler((call) async {
    if (call.method == 'onTap' && call.arguments is String) {
      _taps.add(call.arguments as String);
    }
    return null;
  });
}

/// The APNs device token as a hex string. Throws when registration never
/// produced one, for example because the user denied permission.
Future<String> apnsToken() async {
  final token = await _channel
      .invokeMethod<String>('getApnsToken')
      .timeout(const Duration(seconds: 10));
  if (token == null || token.isEmpty) throw StateError('APNs token unavailable');
  return token;
}

/// Writes to the Keychain group shared with the Notification Service Extension,
/// which decrypts. Values are base64url unpadded.
Future<bool> writeWebPushKeysToKeychain({
  required String privB64url,
  required String authB64url,
}) async {
  final ok = await _channel.invokeMethod<bool>(
    'writeWebPushKeys',
    {'priv': privB64url, 'auth': authB64url},
  );
  return ok ?? false;
}

/// The session id of a notification tap that cold-launched the app.
Future<String?> apnsLaunchSessionId() =>
    _channel.invokeMethod<String?>('getLaunchSessionId');

/// Dismisses the delivered notification for [sessionId] (a composite
/// `node:session` id). APNs owns the delivered notification's identifier, so the
/// native side finds the match by session id rather than by a known id.
Future<void> dismissApnsSession(String sessionId) =>
    _channel.invokeMethod<void>('dismissSession', sessionId);

/// Tells the native side which session is on screen (a composite `node:session`
/// id), so a foreground push for it is not presented. Null clears.
Future<void> setApnsActiveSession(String? sessionId) =>
    _channel.invokeMethod<void>('setActiveSession', sessionId);
