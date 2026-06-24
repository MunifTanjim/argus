import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

// Hide UnifiedPush's PushMessage; this module uses argus's own PushMessage type.
import 'package:unifiedpush/unifiedpush.dart' hide PushMessage;

import 'notifications.dart';
import 'push_message.dart';
import 'push_provider.dart';

final StreamController<PushTarget> _endpoints =
    StreamController<PushTarget>.broadcast();
PushTarget? _lastEndpoint;

final StreamController<String> _failures = StreamController<String>.broadcast();

/// Endpoints reported by the UnifiedPush distributor. The foreground controller
/// listens to register them with the gateway.
Stream<PushTarget> get unifiedPushEndpoints => _endpoints.stream;

/// Registration failures (the FailedReason name, e.g. VAPID_REQUIRED,
/// ACTION_REQUIRED), surfaced so the UI can explain why registration didn't yield
/// an endpoint.
Stream<String> get unifiedPushFailures => _failures.stream;

/// The most recent endpoint, replayed to a late foreground listener.
PushTarget? get lastUnifiedPushEndpoint => _lastEndpoint;

/// Registers the UnifiedPush callbacks. Must be called from main() so it runs in
/// BOTH the foreground isolate and the headless `--unifiedpush-bg` isolate the
/// distributor spins up when the app is killed — otherwise messages received
/// while killed are never displayed. onMessage shows a local notification (works
/// headless); onNewEndpoint is surfaced for the foreground to register.
Future<void> initUnifiedPush() async {
  await UnifiedPush.initialize(
    onNewEndpoint: (endpoint, _) {
      final t = PushTarget(
        endpoint.url,
        p256dh: endpoint.pubKeySet?.pubKey,
        auth: endpoint.pubKeySet?.auth,
      );
      _lastEndpoint = t;
      _endpoints.add(t);
    },
    onMessage: (message, _) =>
        PushNotifications.instance.show(decodeUnifiedPush(message.content)),
    onRegistrationFailed: (reason, _) => _failures.add(reason.name),
    onUnregistered: (_) => _lastEndpoint = null,
  );
}

/// Decodes a UnifiedPush message body (the gateway's JSON payload) into a
/// PushMessage. The distributor delivers the already-decrypted bytes.
PushMessage decodeUnifiedPush(Uint8List bytes) {
  try {
    final obj = jsonDecode(utf8.decode(bytes)) as Map<String, dynamic>;
    final rawData = obj['data'];
    final data = rawData is Map
        ? rawData.map((k, v) => MapEntry(k.toString(), v.toString()))
        : <String, String>{};
    return PushMessage(
      id: obj['id'] as String?,
      title: obj['title'] as String?,
      body: obj['body'] as String?,
      data: data,
    );
  } catch (_) {
    return PushMessage(body: utf8.decode(bytes, allowMalformed: true));
  }
}
