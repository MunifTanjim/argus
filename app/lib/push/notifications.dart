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
  // Set when the notification platform plugin can't be initialized (e.g. a
  // non-Android platform, or a test/headless context without the plugin). Lets
  // show/cancel degrade to no-ops so opening a session never crashes the UI.
  bool _unavailable = false;

  // The session whose detail view is currently open in the foreground, if any.
  // Its notifications are suppressed (and dismissed on open) so a session you are
  // already looking at doesn't also buzz the status bar. This lives only in the
  // UI isolate; the background (app-killed) isolate keeps its own always-null
  // copy, so a killed app still notifies normally.
  String? _activeSessionId;

  /// The session whose view is currently open, or null.
  String? get activeSessionId => _activeSessionId;

  /// Marks [sessionId] as the actively viewed session (null when none). While
  /// set, [show] suppresses notifications for that session.
  void setActiveSession(String? sessionId) => _activeSessionId = sessionId;

  static const AndroidNotificationChannel _channel = AndroidNotificationChannel(
    'argus_sessions',
    'Session alerts',
    description: 'Alerts when a session needs your attention',
    importance: Importance.high,
  );

  /// Session ids of tapped notifications.
  Stream<String> get taps => _taps.stream;

  Future<void> init() async {
    if (_ready || _unavailable) return;
    const InitializationSettings settings = InitializationSettings(
      // Monochrome status-bar icon (Android masks small icons to alpha; a
      // full-color launcher icon would render as a white blob).
      android: AndroidInitializationSettings('ic_stat_argus'),
    );
    try {
      await _plugin.initialize(
        settings,
        onDidReceiveNotificationResponse: (resp) => _emitTap(resp.payload),
      );
      await _plugin
          .resolvePlatformSpecificImplementation<
              AndroidFlutterLocalNotificationsPlugin>()
          ?.createNotificationChannel(_channel);
      _ready = true;
    } catch (_) {
      // No notification platform implementation available; degrade to no-op
      // rather than crashing callers (e.g. SessionDetailScreen on open).
      _unavailable = true;
    }
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
    if (!_ready) return;
    // Drop deliveries the UnifiedPush Android plugin replays to a fresh engine
    // on app relaunch (its event flow buffers the last events with replay=20),
    // which would otherwise re-raise an already-shown notification.
    if (!await _seen.markSeen(m.id)) return;
    // Don't raise a notification for the session the user is actively viewing.
    if (suppressForActive(_activeSessionId, m)) return;
    final id = sessionNotificationId(m.sessionId ?? m.title ?? m.body ?? '');
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

  /// Dismisses the standing notification for [sessionId], if any. Called when a
  /// session's view opens so its alert clears as you start reading.
  Future<void> cancelForSession(String sessionId) async {
    await init();
    if (!_ready) return;
    await _plugin.cancel(sessionNotificationId(sessionId));
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

/// The notification id used for [sessionId]. [PushNotifications.show] reuses it so
/// a session only ever has one notification, and [PushNotifications.cancelForSession]
/// can target it.
///
/// Uses a deterministic FNV-1a hash rather than [String.hashCode]: hashCode is
/// not stable across isolates or runs, so the headless background isolate (which
/// shows notifications when the app is killed) would otherwise pick a different
/// id than the foreground isolate for the same session — stacking duplicates and
/// defeating cancellation. FNV-1a depends only on the bytes, so every isolate
/// agrees. Masked to a positive 31-bit int to stay within Android's int id range.
int sessionNotificationId(String sessionId) {
  var hash = 0x811c9dc5;
  for (final byte in utf8.encode(sessionId)) {
    // Mask to 32 bits each round so the result matches a standard FNV-1a-32
    // (Dart's native int is 64-bit and would not wrap on its own).
    hash = ((hash ^ byte) * 0x01000193) & 0xffffffff;
  }
  return hash & 0x7fffffff;
}

/// Whether a push for [m] should be suppressed because its session is the one
/// currently open on screen ([activeSessionId]).
bool suppressForActive(String? activeSessionId, PushMessage m) =>
    activeSessionId != null && m.sessionId == activeSessionId;
