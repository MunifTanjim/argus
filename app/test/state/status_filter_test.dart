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

  test('toggle sets and clears', () {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final n = c.read(statusFilterProvider.notifier);
    n.toggle(TileStatus.waiting);
    expect(c.read(statusFilterProvider), TileStatus.waiting);
    n.toggle(TileStatus.blocked);
    expect(c.read(statusFilterProvider), TileStatus.blocked);
    n.toggle(TileStatus.blocked);
    expect(c.read(statusFilterProvider), isNull);
    n.toggle(TileStatus.working);
    n.clear();
    expect(c.read(statusFilterProvider), isNull);
  });
}
