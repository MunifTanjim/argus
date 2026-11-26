import 'dart:convert';

import 'package:argus/models/session.dart';
import 'package:argus/state/status_filter.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

Session _s(String id, String status, {bool prompt = false}) =>
    Session.fromJson(jsonDecode(jsonEncode({
      'id': id,
      'agent': 'claude',
      'status': status,
      'source': 'hooked',
      'tmux': {
        'server': 'argus',
        'pane_id': '%1',
        'session_name': 's',
        'window_index': 0,
        'current_path': '/p',
      },
      if (prompt) 'interaction': {'kind': 'permission', 'tool_name': 'Bash'},
    })) as Map<String, dynamic>);

void main() {
  final blocked = _s('a', 'awaiting_input', prompt: true);
  final waiting = _s('b', 'awaiting_input');
  final working = _s('c', 'working');
  final starting = _s('d', 'starting');
  final idle = _s('e', 'idle');

  test('tileStatusOf maps each status', () {
    expect(tileStatusOf(blocked), TileStatus.blocked);
    expect(tileStatusOf(waiting), TileStatus.waiting);
    expect(tileStatusOf(working), TileStatus.working);
    expect(tileStatusOf(starting), TileStatus.working);
    expect(tileStatusOf(idle), isNull);
  });

  test('tileCounts counts each tile', () {
    expect(tileCounts([blocked, waiting, working, starting, idle]), {
      TileStatus.blocked: 1,
      TileStatus.waiting: 1,
      TileStatus.working: 2,
    });
    expect(tileCounts(const []).values, everyElement(0));
  });

  test('toggle adds and removes; scopes are separate', () {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final home = c.read(statusFilterProvider(homeStatusScope).notifier);
    expect(c.read(statusFilterProvider(homeStatusScope)), isEmpty);
    home.toggle(TileStatus.waiting);
    home.toggle(TileStatus.blocked);
    expect(c.read(statusFilterProvider(homeStatusScope)),
        {TileStatus.waiting, TileStatus.blocked});
    home.toggle(TileStatus.waiting);
    expect(c.read(statusFilterProvider(homeStatusScope)), {TileStatus.blocked});
    expect(c.read(statusFilterProvider('mac:ws1')), isEmpty);
    home.clear();
    expect(c.read(statusFilterProvider(homeStatusScope)), isEmpty);
  });
}
