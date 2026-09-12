import 'dart:math' show min;

const pushMaxRenewLead = Duration(hours: 6);

/// [PushLease.none] means the backend owns the endpoint's lifetime and the app
/// never has to watch the clock.
class PushLease {
  const PushLease({required this.expiresAt, required this.renewAt});

  /// A lead as long as the lease would put every subscription past its renewal
  /// point the moment it was minted, so every reconnect would mint another.
  factory PushLease.granted(int expiresAt, {DateTime? now}) {
    if (expiresAt <= 0) return none;
    final span = expiresAt - _unix(now ?? DateTime.now());
    if (span <= 0) return PushLease(expiresAt: expiresAt, renewAt: expiresAt);
    final lead = min(span ~/ 4, pushMaxRenewLead.inSeconds);
    return PushLease(expiresAt: expiresAt, renewAt: expiresAt - lead);
  }

  /// The grant instant is not persisted, so the early renewal point is gone. A
  /// restored lease belongs to no running provider, so the only question it
  /// answers is whether the endpoint is dead.
  factory PushLease.restored(int expiresAt) => expiresAt <= 0
      ? none
      : PushLease(expiresAt: expiresAt, renewAt: expiresAt);

  static const none = PushLease(expiresAt: 0, renewAt: 0);

  /// Unix seconds. Zero when no expiry is known.
  final int expiresAt;

  /// Unix seconds. Zero when no expiry is known.
  final int renewAt;

  bool get isKnown => expiresAt > 0;

  bool needsRenewal({DateTime? now}) =>
      isKnown && _unix(now ?? DateTime.now()) >= renewAt;

  bool hasExpired({DateTime? now}) =>
      isKnown && _unix(now ?? DateTime.now()) >= expiresAt;

  @override
  bool operator ==(Object other) =>
      other is PushLease &&
      other.expiresAt == expiresAt &&
      other.renewAt == renewAt;

  @override
  int get hashCode => Object.hash(expiresAt, renewAt);

  @override
  String toString() => 'PushLease(expiresAt: $expiresAt, renewAt: $renewAt)';
}

int _unix(DateTime t) => t.millisecondsSinceEpoch ~/ 1000;
