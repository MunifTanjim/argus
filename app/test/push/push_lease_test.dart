import 'package:argus/push/push_lease.dart';
import 'package:flutter_test/flutter_test.dart';

import 'push_test_support.dart';

final _now = DateTime.utc(2026, 1, 1, 12);

int _unixIn(Duration d) => unixIn(d, now: _now);

DateTime _fromUnix(int seconds) =>
    DateTime.fromMillisecondsSinceEpoch(seconds * 1000, isUtc: true);

void main() {
  test('none knows no expiry and is never due', () {
    expect(PushLease.none.isKnown, isFalse);
    expect(PushLease.none.needsRenewal(now: _now), isFalse);
    expect(PushLease.none.hasExpired(now: _now), isFalse);
  });

  test('an absent or negative expiry is no lease at all', () {
    expect(PushLease.granted(0, now: _now), PushLease.none);
    expect(PushLease.granted(-1, now: _now), PushLease.none);
  });

  test('a long lease renews six hours before expiry', () {
    final lease = PushLease.granted(_unixIn(const Duration(days: 30)), now: _now);
    expect(lease.expiresAt - lease.renewAt, const Duration(hours: 6).inSeconds);
  });

  test('a 24 hour lease renews six hours before expiry', () {
    final lease = PushLease.granted(_unixIn(const Duration(hours: 24)), now: _now);
    expect(lease.expiresAt - lease.renewAt, const Duration(hours: 6).inSeconds);
  });

  test('a short lease renews after a quarter of it, not six hours in', () {
    final lease = PushLease.granted(_unixIn(const Duration(hours: 4)), now: _now);
    expect(lease.expiresAt - lease.renewAt, const Duration(hours: 1).inSeconds);
    expect(lease.needsRenewal(now: _now), isFalse,
        reason: 'a fresh lease must not be due the second it is granted');
  });

  test('renewal is due from renewAt onward, and not one second before', () {
    final lease = PushLease.granted(_unixIn(const Duration(hours: 24)), now: _now);
    final renewAt = _fromUnix(lease.renewAt);
    expect(lease.needsRenewal(now: renewAt.subtract(const Duration(seconds: 1))),
        isFalse);
    expect(lease.needsRenewal(now: renewAt), isTrue);
    expect(lease.needsRenewal(now: renewAt.add(const Duration(hours: 1))), isTrue);
  });

  test('expiry is reached at expiresAt, and not one second before', () {
    final lease = PushLease.granted(_unixIn(const Duration(hours: 24)), now: _now);
    final expiry = _fromUnix(lease.expiresAt);
    expect(lease.hasExpired(now: expiry.subtract(const Duration(seconds: 1))),
        isFalse);
    expect(lease.hasExpired(now: expiry), isTrue);
  });

  test('a lease granted after its own expiry is due at once', () {
    final lease = PushLease.granted(_unixIn(const Duration(hours: -1)), now: _now);
    expect(lease.renewAt, lease.expiresAt);
    expect(lease.needsRenewal(now: _now), isTrue);
    expect(lease.hasExpired(now: _now), isTrue);
  });

  test('a restored lease keeps the expiry and renews no earlier', () {
    final expiresAt = _unixIn(const Duration(hours: 24));
    final restored = PushLease.restored(expiresAt);
    expect(restored.expiresAt, expiresAt);
    expect(restored.renewAt, expiresAt,
        reason: 'the grant instant is gone, so there is no early renewal point');
    expect(restored.hasExpired(now: _now), isFalse);
    expect(restored.hasExpired(now: _fromUnix(expiresAt)), isTrue);
  });

  test('a restored lease with no expiry is none', () {
    expect(PushLease.restored(0), PushLease.none);
    expect(PushLease.restored(-1), PushLease.none);
  });
}
