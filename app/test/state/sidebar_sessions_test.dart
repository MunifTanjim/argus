import 'dart:convert';

import 'package:argus/models/session.dart';
import 'package:argus/state/sidebar_sessions.dart';
import 'package:flutter_test/flutter_test.dart';

Session _s(
  String id,
  String status, {
  String? activity,
  bool offline = false,
  String? name,
  String? kind,
}) => Session.fromJson(
  jsonDecode(
        jsonEncode({
          'id': id,
          'agent': 'claude',
          'status': status,
          'source': 'hooked',
          'offline': offline,
          if (name case final _?) 'name': name,
          'tmux': {
            'server': 'argus',
            'pane_id': '%1',
            'session_name': 'tmux-$id',
            'window_index': 0,
            'current_path': '/p',
          },
          if (activity case final _?) 'summary': {'last_activity': activity},
          if (kind case final _?) 'interaction': {'kind': kind},
        }),
      )
      as Map<String, dynamic>,
);

void main() {
  test('needs you lists blocked sessions first', () {
    final groups = buildSidebarSections([
      _s('wait', 'awaiting_input', activity: '2026-10-11T09:00:00Z', kind: 'idle'),
      _s('block', 'awaiting_input', activity: '2026-10-11T08:00:00Z', kind: 'question'),
      _s('work2', 'working', activity: '2026-10-11T09:00:00Z'),
      _s('work1', 'working', activity: '2026-10-11T08:00:00Z'),
    ]);
    expect(groups.map((g) => g.sessions.map((s) => s.id).toList()), [
      ['block', 'wait'],
      ['work2', 'work1'],
    ]);
  });

  test('sections in order, newest first, dead hidden', () {
    final groups = buildSidebarSections([
      _s('idle-old', 'idle', activity: '2026-10-11T08:00:00Z'),
      _s('idle-new', 'idle', activity: '2026-10-11T09:00:00+02:00'),
      _s('disc', 'discovered'),
      _s('work', 'working', activity: '2026-10-11T07:00:00Z'),
      _s('start', 'starting', activity: '2026-10-11T07:30:00Z'),
      _s('wait', 'awaiting_input'),
      _s('dead', 'dead', activity: '2026-10-11T10:00:00Z'),
      _s('off-wait', 'awaiting_input', offline: true),
      _s('off-dead', 'dead', offline: true),
      _s('bad-time', 'idle', activity: 'yesterday'),
    ]);
    expect(groups.length, 4);
    expect(groups[0].section, SidebarSection.needsYou);
    expect([for (final s in groups[0].sessions) s.id], ['wait']);
    expect(groups[1].section, SidebarSection.working);
    expect([for (final s in groups[1].sessions) s.id], ['start', 'work']);
    expect(groups[2].section, SidebarSection.idle);
    expect(
      [for (final s in groups[2].sessions) s.id],
      ['idle-old', 'idle-new', 'bad-time', 'disc'],
    );
    expect(groups[3].section, SidebarSection.offline);
    expect([for (final s in groups[3].sessions) s.id], ['off-wait']);
  });

  test('empty sections are left out', () {
    expect(buildSidebarSections([]), isEmpty);
    final groups = buildSidebarSections([_s('a', 'idle')]);
    expect(groups.map((g) => g.section), [SidebarSection.idle]);
  });

  test('title: name, then tmux session, then agent, then id', () {
    expect(sidebarTitle(_s('a', 'idle', name: 'fix bug')), 'fix bug');
    expect(sidebarTitle(_s('a', 'idle')), 'tmux-a');
  });

  test('matching: home-list fields, title, and place', () {
    final s = _s('a', 'idle', name: 'fix bug');
    expect(sidebarSessionMatches(s, '', ''), isTrue);
    expect(sidebarSessionMatches(s, 'FIX', ''), isTrue);
    expect(sidebarSessionMatches(s, 'argus / feat', 'argus / feat-x'), isTrue);
    expect(sidebarSessionMatches(_s('b', 'idle'), 'tmux-b', ''), isTrue);
    expect(sidebarSessionMatches(s, 'zzz', 'argus / main'), isFalse);
  });
}
