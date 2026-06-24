import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/relative_time.dart';

void main() {
  // Fixed reference point for deterministic tests
  final now = DateTime.utc(2026, 1, 1, 12, 0, 0);

  group('relativeTime', () {
    test('null input returns empty string', () {
      expect(relativeTime(null, now), '');
    });

    test('empty string returns empty string', () {
      expect(relativeTime('', now), '');
    });

    test('unparseable string returns empty string', () {
      expect(relativeTime('nope', now), '');
    });

    test('10 seconds earlier returns just now', () {
      final then = now.subtract(const Duration(seconds: 10));
      expect(relativeTime(then.toIso8601String(), now), 'just now');
    });

    test('90 seconds earlier returns 1m ago', () {
      final then = now.subtract(const Duration(seconds: 90));
      expect(relativeTime(then.toIso8601String(), now), '1m ago');
    });

    test('3 hours earlier returns 3h ago', () {
      final then = now.subtract(const Duration(hours: 3));
      expect(relativeTime(then.toIso8601String(), now), '3h ago');
    });

    test('2 days earlier returns 2d ago', () {
      final then = now.subtract(const Duration(days: 2));
      expect(relativeTime(then.toIso8601String(), now), '2d ago');
    });

    test('10 days earlier returns 1w ago', () {
      final then = now.subtract(const Duration(days: 10));
      expect(relativeTime(then.toIso8601String(), now), '1w ago');
    });

    test('45 days earlier returns 1mo ago', () {
      final then = now.subtract(const Duration(days: 45));
      expect(relativeTime(then.toIso8601String(), now), '1mo ago');
    });

    test('400 days earlier returns 1y ago', () {
      final then = now.subtract(const Duration(days: 400));
      expect(relativeTime(then.toIso8601String(), now), '1y ago');
    });

    test('future timestamp clamps to just now', () {
      final then = now.add(const Duration(hours: 1));
      expect(relativeTime(then.toIso8601String(), now), 'just now');
    });

    test('uses DateTime.now() when now is omitted', () {
      // Just verify it doesn't throw and returns a non-null string
      final result = relativeTime(DateTime.now().toIso8601String());
      expect(result, isA<String>());
    });
  });
}
