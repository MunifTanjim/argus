import 'push_message.dart';

/// A device's Web Push target: the distributor endpoint URL the gateway POSTs to,
/// plus the subscription keys (present for Web Push distributors; null for plain
/// endpoints). [toParams] is merged into the push.register RPC payload.
class PushTarget {
  const PushTarget(this.endpoint, {this.p256dh, this.auth});

  final String endpoint;
  final String? p256dh;
  final String? auth;

  Map<String, dynamic> toParams() => {
        'endpoint': endpoint,
        if (p256dh != null) 'p256dh': p256dh,
        if (auth != null) 'auth': auth,
      };

  @override
  bool operator ==(Object other) =>
      other is PushTarget &&
      other.endpoint == endpoint &&
      other.p256dh == p256dh &&
      other.auth == auth;

  @override
  int get hashCode => Object.hash(endpoint, p256dh, auth);
}

/// A push backend: registers the device with its distributor, reports the
/// resulting [PushTarget] (which can change), and routes incoming pushes.
abstract class PushProvider {
  String get name;

  /// Whether this backend can run on the device.
  Future<bool> isAvailable();

  /// Start receiving. [onTarget] fires whenever the device target becomes known
  /// or changes; the controller re-registers it with the gateway each time.
  /// [onMessage] is an incoming push to display. [onOpen] is a push the user
  /// tapped to open the app (deep-link, not displayed).
  Future<void> start({
    required void Function(PushTarget) onTarget,
    required void Function(PushMessage) onMessage,
    required void Function(PushMessage) onOpen,
  });

  /// Re-request the current endpoint from the distributor so [onTarget] fires
  /// again. Used when the controller has no target after a restart/connect and
  /// needs the backend to surface one.
  Future<void> refresh();

  /// Stop receiving and forget the target (called on unpair).
  Future<void> stop();
}
