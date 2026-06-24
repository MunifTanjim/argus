import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/grouping.dart';

Session _s(String id, String host, String status, {bool offline = false}) =>
    Session.fromJson(jsonDecode(
        '{"id":"$id","tool":"t","status":"$status","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"node_label":"$host","offline":$offline}'));

Session _sn(String id, {String? nodeId, String? nodeLabel}) =>
    Session.fromJson(jsonDecode(jsonEncode({
      'id': id,
      'tool': 't',
      'status': 'working',
      'source': 'hooked',
      'tmux': {
        'server': 'argus',
        'pane_id': '%1',
        'session_name': 's',
        'window_index': 0,
        'current_path': '/p',
      },
      'node_id': ?nodeId,
      'node_label': ?nodeLabel,
    })) as Map<String, dynamic>);

void main() {
  test('needs-you section is pinned first and aggregates awaiting', () {
    final sections = buildSections([
      _s('dev:1', 'dev', 'working'),
      _s('home:1', 'home', 'awaiting_input'),
      _s('dev:2', 'dev', 'awaiting_input'),
    ]);
    expect(sections.first.needsYou, isTrue);
    expect(sections.first.title, 'Needs you');
    expect(sections.first.sessions.map((s) => s.id),
        ['dev:2', 'home:1']); // sorted by host then id
  });

  test('host sections exclude awaiting, sorted by title and id', () {
    final sections = buildSections([
      _s('dev:2', 'dev', 'idle'),
      _s('dev:1', 'dev', 'working'),
      _s('alpha:1', 'alpha', 'working'),
      _s('home:1', 'home', 'awaiting_input'),
    ]);
    final hosts = sections.where((s) => !s.needsYou).toList();
    expect(hosts.map((s) => s.title), ['alpha', 'dev']);
    expect(hosts[1].sessions.map((s) => s.id), ['dev:1', 'dev:2']);
  });

  test('host offline only when all sessions offline', () {
    final sections = buildSections([
      _s('dev:1', 'dev', 'idle', offline: true),
      _s('dev:2', 'dev', 'idle', offline: true),
      _s('home:1', 'home', 'idle', offline: true),
      _s('home:2', 'home', 'working'),
    ]);
    final dev = sections.firstWhere((s) => s.title == 'dev');
    final home = sections.firstWhere((s) => s.title == 'home');
    expect(dev.offline, isTrue);
    expect(home.offline, isFalse);
  });

  test('empty input yields no sections', () {
    expect(buildSections(const []), isEmpty);
  });

  group('nodesFromSessions', () {
    test('empty input yields empty list', () {
      expect(nodesFromSessions([]), isEmpty);
    });

    test('sessions with null nodeId are skipped', () {
      final sessions = [_sn('a'), _sn('b')];
      expect(nodesFromSessions(sessions), isEmpty);
    });

    test('sessions with empty nodeId are skipped', () {
      final sessions = [_sn('a', nodeId: '')];
      expect(nodesFromSessions(sessions), isEmpty);
    });

    test('two sessions with same nodeId produce one NodeRef', () {
      final sessions = [
        _sn('a', nodeId: 'node1', nodeLabel: 'My Node'),
        _sn('b', nodeId: 'node1', nodeLabel: 'My Node'),
      ];
      final result = nodesFromSessions(sessions);
      expect(result, hasLength(1));
      expect(result.first.id, 'node1');
      expect(result.first.label, 'My Node');
    });

    test('label falls back to nodeId when nodeLabel is null', () {
      final sessions = [_sn('a', nodeId: 'node1')];
      final result = nodesFromSessions(sessions);
      expect(result.first.label, 'node1');
    });

    test('deduplication keeps first label', () {
      final sessions = [
        _sn('a', nodeId: 'node1', nodeLabel: 'First Label'),
        _sn('b', nodeId: 'node1', nodeLabel: 'Second Label'),
      ];
      final result = nodesFromSessions(sessions);
      expect(result.first.label, 'First Label');
    });

    test('result is sorted by label', () {
      final sessions = [
        _sn('a', nodeId: 'n2', nodeLabel: 'Zebra'),
        _sn('b', nodeId: 'n1', nodeLabel: 'Alpha'),
        _sn('c', nodeId: 'n3', nodeLabel: 'Mango'),
      ];
      final result = nodesFromSessions(sessions);
      expect(result.map((n) => n.label), ['Alpha', 'Mango', 'Zebra']);
    });

    test('mixed null/empty and valid nodeIds', () {
      final sessions = [
        _sn('a'),
        _sn('b', nodeId: ''),
        _sn('c', nodeId: 'valid', nodeLabel: 'Valid Node'),
      ];
      final result = nodesFromSessions(sessions);
      expect(result, hasLength(1));
      expect(result.first.id, 'valid');
    });
  });
}
