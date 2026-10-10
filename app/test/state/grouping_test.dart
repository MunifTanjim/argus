import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/project.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/grouping.dart';

Session _s(String id, String host, String status, {bool offline = false}) =>
    Session.fromJson(jsonDecode(
        '{"id":"$id","agent":"t","status":"$status","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"node_label":"$host","offline":$offline}'));

Session _sn(String id, {String? nodeId, String? nodeLabel}) =>
    Session.fromJson(jsonDecode(jsonEncode({
      'id': id,
      'agent': 't',
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

Session _g(String id, String host, String status,
        {String? ws, String agent = 't', bool offline = false}) =>
    Session.fromJson(jsonDecode(jsonEncode({
      'id': id,
      'agent': agent,
      'status': status,
      'source': 'hooked',
      'tmux': {
        'server': 'argus',
        'pane_id': '%1',
        'session_name': 's',
        'window_index': 0,
        'current_path': '/p',
      },
      if (host.isNotEmpty) 'node_id': host,
      if (host.isNotEmpty) 'node_label': host,
      'workspace_id': ?ws,
      'offline': offline,
    })) as Map<String, dynamic>);

const _projects = [
  ProjectNode(id: 'a:p1', name: 'argus', nodeId: 'a', workspaces: [
    WorkspaceNode(id: 'a:w1', dir: '/src/argus', isMain: true),
    WorkspaceNode(id: 'a:w2', dir: '/src/argus-feat'),
    WorkspaceNode(id: 'a:w5', dir: '/src/.worktrees/aaa'),
    WorkspaceNode(id: 'a:w6', dir: '/src/.worktrees/zzz'),
  ]),
  ProjectNode(id: 'b:p1', name: 'argus', nodeId: 'b', workspaces: [
    WorkspaceNode(id: 'b:w1', dir: '/src/argus', isMain: true),
  ]),
  ProjectNode(id: 'a:p4', name: 'notes', kind: 'plain', nodeId: 'a', workspaces: [
    WorkspaceNode(id: 'a:w7', dir: '/src/notes'),
  ]),
  ProjectNode(id: 'a:p3', name: 'zeta', nodeId: 'a', workspaces: [
    WorkspaceNode(id: 'a:w4', dir: '/src/zeta', isMain: true),
  ]),
];

List<String> _titles(List<SessionSection> s) => [for (final x in s) x.title];

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

  group('group-by', () {
    test('project merges by name across hosts; other is last', () {
      final s = buildSections([
        _g('b:1', 'beta', 'idle', ws: 'b:w1'),
        _g('a:1', 'alpha', 'idle', ws: 'a:w1'),
        _g('a:3', 'alpha', 'idle', ws: 'a:w4'),
        _g('a:9', 'alpha', 'idle'),
        _g('a:8', 'alpha', 'idle', ws: 'a:gone'),
      ], by: GroupBy.project, projects: _projects);
      expect(_titles(s), ['argus', 'zeta', 'other']);
      expect(s[0].sessions.map((x) => x.id), ['a:1', 'b:1']);
      expect(s[0].showNode, isTrue);
      expect(s[0].icon, SectionIcon.project);
    });

    test('workspace stays per host and names it on a gateway', () {
      final s = buildSections([
        _g('b:1', 'beta', 'idle', ws: 'b:w1'),
        _g('a:1', 'alpha', 'idle', ws: 'a:w1'),
        _g('a:2', 'alpha', 'idle', ws: 'a:w2'),
      ], by: GroupBy.workspace, projects: _projects);
      expect(_titles(s), [
        'argus / argus · alpha',
        'argus / argus · beta',
        'argus / argus-feat · alpha',
      ]);
      expect(s.every((x) => !x.showNode), isTrue);
    });

    test('workspace without a gateway has no host suffix', () {
      final s = buildSections([_g('s1', '', 'idle', ws: 'a:w1')],
          by: GroupBy.workspace, projects: _projects);
      expect(_titles(s), ['argus / argus']);
    });

    test('workspaces of a project stay together, main first', () {
      final s = buildSections([
        _g('s1', '', 'idle', ws: 'a:w6'),
        _g('s2', '', 'idle', ws: 'a:w4'),
        _g('s3', '', 'idle', ws: 'a:w5'),
        _g('s4', '', 'idle', ws: 'a:w2'),
        _g('s5', '', 'idle', ws: 'a:w1'),
      ], by: GroupBy.workspace, projects: _projects);
      expect(_titles(s), [
        'argus / argus',
        'argus / aaa',
        'argus / argus-feat',
        'argus / zzz',
        'zeta / zeta',
      ]);
    });

    test('agent uses display names', () {
      final s = buildSections([
        _g('a:1', 'alpha', 'idle', agent: 'codex'),
        _g('a:2', 'alpha', 'idle', agent: 'claude'),
      ], by: GroupBy.agent);
      expect(_titles(s), ['Claude', 'Codex']);
    });

    test('status in lifecycle order after needs you', () {
      final s = buildSections([
        _g('a:1', 'alpha', 'dead'),
        _g('a:2', 'alpha', 'idle'),
        _g('a:3', 'alpha', 'working'),
        _g('a:4', 'alpha', 'awaiting_input'),
      ], by: GroupBy.status);
      expect(_titles(s), ['Needs you', 'working', 'idle', 'dead']);
      expect(s.first.showNode, isTrue);
    });

    test('host sections name the host; cards do not', () {
      final s = buildSections([_g('a:1', 'alpha', 'idle')]);
      expect(s.single.icon, SectionIcon.host);
      expect(s.single.showNode, isFalse);
    });

    test('each section names its icon; other takes the dimension\'s', () {
      List<(String, SectionIcon, bool)> icons(GroupBy by, List<Session> ss) => [
        for (final x in buildSections(ss, by: by, projects: _projects))
          (x.title, x.icon, x.other),
      ];
      expect(icons(GroupBy.host, [
        _g('a:1', 'alpha', 'awaiting_input'),
        _g('a:2', 'alpha', 'idle'),
      ]), [
        ('Needs you', SectionIcon.needsYou, false),
        ('alpha', SectionIcon.host, false),
      ]);
      expect(icons(GroupBy.project, [
        _g('s1', '', 'idle', ws: 'a:w1'),
        _g('s2', '', 'idle'),
      ]), [
        ('argus', SectionIcon.project, false),
        ('other', SectionIcon.project, true),
      ]);
      expect(icons(GroupBy.workspace, [
        _g('s1', '', 'idle', ws: 'a:w1'),
        _g('s2', '', 'idle', ws: 'a:w2'),
        _g('s3', '', 'idle', ws: 'a:w7'),
        _g('s4', '', 'idle'),
      ]), [
        ('argus / argus', SectionIcon.folder, false),
        ('argus / argus-feat', SectionIcon.branch, false),
        ('notes / notes', SectionIcon.folder, false),
        ('other', SectionIcon.folder, true),
      ]);
      expect(icons(GroupBy.agent, [
        _g('s1', '', 'idle', agent: 'claude'),
        _g('s2', '', 'idle', agent: ''),
      ]), [
        ('Claude', SectionIcon.agent, false),
        ('other', SectionIcon.agent, true),
      ]);
      expect(icons(GroupBy.status, [_g('s1', '', 'idle')]), [
        ('idle', SectionIcon.status, false),
      ]);
    });

    test('offline applies to any section', () {
      final s = buildSections([
        _g('a:1', 'alpha', 'idle', agent: 'claude', offline: true),
      ], by: GroupBy.agent);
      expect(s.single.offline, isTrue);
    });
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
