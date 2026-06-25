import 'dart:async';
import 'dart:convert';

import 'package:flutter_local_notifications/flutter_local_notifications.dart';

import 'push_message.dart';
import 'seen_messages.dart';

/// PushNotifications renders push payloads as Android system notifications and
/// surfaces taps as session ids. Both push backends feed it, so display and
/// deep-linking live in one place. Foreground messages (and UnifiedPush messages,
/// which never auto-display) are shown here; FCM notification messages are shown
/// by the OS directly when the app is backgrounded/killed.
class PushNotifications {
  PushNotifications._();
  static final PushNotifications instance = PushNotifications._();

  final FlutterLocalNotificationsPlugin _plugin =
      FlutterLocalNotificationsPlugin();
  final StreamController<String> _taps = StreamController<String>.broadcast();
  final SeenMessages _seen = SeenMessages();
  bool _ready = false;

  static const AndroidNotificationChannel _channel = AndroidNotificationChannel(
    'argus_sessions',
    'Session alerts',
    description: 'Alerts when a session needs your attention',
    importance: Importance.high,
  );

  /// Session ids of tapped notifications.
  Stream<String> get taps => _taps.stream;

  Future<void> init() async {
    if (_ready) return;
    const InitializationSettings settings = InitializationSettings(
      // Monochrome status-bar icon (Android masks small icons to alpha; a
      // full-color launcher icon would render as a white blob).
      android: AndroidInitializationSettings('ic_stat_argus'),
    );
    await _plugin.initialize(
      settings,
      onDidReceiveNotificationResponse: (resp) => _emitTap(resp.payload),
    );
    await _plugin
        .resolvePlatformSpecificImplementation<
            AndroidFlutterLocalNotificationsPlugin>()
        ?.createNotificationChannel(_channel);
    _ready = true;
  }

  /// Requests the Android 13+ POST_NOTIFICATIONS runtime permission.
  Future<bool> requestPermission() async {
    final android = _plugin.resolvePlatformSpecificImplementation<
        AndroidFlutterLocalNotificationsPlugin>();
    return await android?.requestNotificationsPermission() ?? true;
  }

  /// The session id of a notification that cold-launched the app, if any.
  Future<String?> launchSessionId() async {
    final details = await _plugin.getNotificationAppLaunchDetails();
    if (details?.didNotificationLaunchApp ?? false) {
      return _sessionFromPayload(details!.notificationResponse?.payload);
    }
    return null;
  }

  Future<void> show(PushMessage m) async {
    await init();
    // Drop deliveries the UnifiedPush Android plugin replays to a fresh engine
    // on app relaunch (its event flow buffers the last events with replay=20),
    // which would otherwise re-raise an already-shown notification.
    if (!await _seen.markSeen(m.id)) return;
    final id = (m.sessionId ?? m.title ?? m.body ?? '').hashCode;
    await _plugin.show(
      id,
      m.title ?? 'argus',
      m.body ?? '',
      NotificationDetails(
        android: AndroidNotificationDetails(
          _channel.id,
          _channel.name,
          channelDescription: _channel.description,
          importance: Importance.high,
          priority: Priority.high,
        ),
      ),
      payload: jsonEncode(m.data),
    );
  }

  void _emitTap(String? payload) {
    final id = _sessionFromPayload(payload);
    if (id != null) _taps.add(id);
  }

  String? _sessionFromPayload(String? payload) {
    if (payload == null || payload.isEmpty) return null;
    try {
      final data = (jsonDecode(payload) as Map).cast<String, dynamic>();
      return data['session_id'] as String?;
    } catch (_) {
      return null;
    }
  }
}
