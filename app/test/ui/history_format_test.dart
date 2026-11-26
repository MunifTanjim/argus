import 'package:argus/ui/history_format.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  final now = DateTime(2026, 11, 26, 15, 0); // Thursday

  String ago(Duration d) => now.subtract(d).toIso8601String();

  test('shortAgo', () {
    expect(shortAgo(null, now), '');
    expect(shortAgo('', now), '');
    expect(shortAgo('x', now), '');
    expect(shortAgo(ago(const Duration(seconds: 20)), now), 'now');
    expect(shortAgo(ago(const Duration(hours: 2)), now), '2h');
    expect(shortAgo(ago(const Duration(days: 3)), now), '3d');
    expect(shortAgo(ago(const Duration(days: 125)), now), '4mo');
    expect(shortAgo(ago(const Duration(days: 400)), now), '1y');
  });

  test('dayLabel', () {
    expect(dayLabel(DateTime(2026, 11, 26, 0, 5), now), 'Today');
    expect(dayLabel(DateTime(2026, 11, 25, 23, 59), now), 'Yesterday');
    expect(dayLabel(DateTime(2026, 11, 23, 10), now), 'Monday');
    expect(dayLabel(DateTime(2026, 11, 20, 10), now), 'Friday');
    expect(dayLabel(DateTime(2026, 11, 19, 10), now), 'Nov 19');
    expect(dayLabel(DateTime(2025, 12, 3), now), 'Dec 3, 2025');
  });

  test('clockTime', () {
    expect(clockTime(DateTime(2026, 1, 1, 9, 5)), '09:05');
    expect(clockTime(DateTime(2026, 1, 1, 14, 32)), '14:32');
  });

  test('compactDuration', () {
    expect(compactDuration(0), '');
    expect(compactDuration(-5), '');
    expect(compactDuration(45000), '45s');
    expect(compactDuration(34 * 60000 + 59000), '34m');
    expect(compactDuration(72 * 60000), '1h 12m');
    expect(compactDuration(60 * 60000), '1h');
  });

  test('shortenPath', () {
    expect(shortenPath('/Users/me/Dev/github/me/argus'), '~/Dev/…/argus');
    expect(shortenPath('/home/me/.dotfiles'), '~/.dotfiles');
    expect(shortenPath('/home/me'), '~');
    expect(shortenPath('/srv/cmp-server'), '/srv/cmp-server');
    expect(shortenPath('/a/b/c/d/e'), '/a/…/e');
    expect(shortenPath(''), '');
  });
}
