import 'package:argus/models/enums.dart';
import 'package:argus/models/project.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/project_tree.dart';
import 'package:flutter_test/flutter_test.dart';

ProjectNode _p(
  String id,
  String name, {
  String node = 'A',
  String label = 'mbp',
  bool hidden = false,
  bool gone = false,
  bool pinned = false,
  String kind = 'git',
  String error = '',
  List<WorkspaceNode> ws = const [],
}) => ProjectNode(
  id: id,
  name: name,
  kind: kind,
  nodeId: node,
  nodeLabel: label,
  hidden: hidden,
  isGone: gone,
  pinned: pinned,
  error: error,
  workspaces: ws,
);

WorkspaceNode _w(
  String id,
  String dir, {
  String branch = '',
  String head = '',
  bool main = false,
  bool gone = false,
  String? setup,
}) => WorkspaceNode(
  id: id,
  dir: dir,
  branch: branch,
  head: head,
  isMain: main,
  isGone: gone,
  setupState: setup,
);

Session _s(
  String id,
  SessionStatus status, {
  String? ws,
  bool offline = false,
}) => Session(
  id: id,
  agent: 'claude',
  tmux: const TmuxLocation(
    server: TmuxServerKind.default_,
    paneId: '',
    sessionName: '',
    windowIndex: 0,
    currentPath: '',
  ),
  status: status,
  source: SessionSource.discovered,
  workspaceId: ws,
  offline: offline,
);

const _all = TreeView();

void main() {
  final argus = _p(
    'A:p1',
    'argus',
    pinned: true,
    ws: [
      _w('A:w1', '/src/argus', branch: 'main', main: true),
      _w(
        'A:w2',
        '/src/argus/.worktrees/registry',
        branch: 'feat/registry',
        setup: 'running',
      ),
      _w('A:w3', '/src/argus/.worktrees/old', branch: 'old', gone: true),
    ],
  );
  final crush = _p(
    'A:p2',
    'crush',
    ws: [_w('A:w4', '/src/crush', head: 'abcdef123456')],
  );

  test('single node: no node row, projects then workspaces', () {
    final rows = buildTreeRows([argus, crush], _all);
    expect(rows.map((r) => r.kind), [
      TreeRowKind.project,
      TreeRowKind.workspace,
      TreeRowKind.workspace,
      TreeRowKind.project,
      TreeRowKind.workspace,
    ]);
    expect(rows[0].pinned, isTrue);
    expect(rows[1].isMain, isTrue);
    expect(rows[1].detail, 'main');
    expect(rows[2].label, 'registry');
    expect(rows[2].setup, SetupMark.running);
    expect(rows[4].detail, 'abcdef1'); // detached: short head
  });

  test('multi node: a node row heads each node, sorted by label', () {
    final infra = _p(
      'B:p1',
      'infra',
      node: 'B',
      label: 'devbox',
      ws: [_w('B:w1', '/infra', branch: 'main')],
    );
    final rows = buildTreeRows([argus, infra], _all);
    expect(rows.first.kind, TreeRowKind.node);
    expect(rows.first.label, 'devbox');
    expect(rows[1].label, 'infra');
    expect(rows.where((r) => r.kind == TreeRowKind.node).map((r) => r.label), [
      'devbox',
      'mbp',
    ]);
  });

  test('multi node: nodes with the same label sort by node id', () {
    final c = _p('C:p1', 'c', node: 'C', label: 'devbox');
    final b = _p('B:p1', 'b', node: 'B', label: 'devbox');
    final rows = buildTreeRows([c, b], _all);
    expect(rows.where((r) => r.kind == TreeRowKind.node).map((r) => r.id), [
      'B',
      'C',
    ]);
  });

  test('projects keep the node\'s order, pinned first', () {
    final zeta = _p('A:p7', 'zeta', pinned: true);
    final alpha = _p('A:p8', 'alpha');
    final rows = buildTreeRows([zeta, alpha], _all);
    expect(rows.map((r) => r.label), ['zeta', 'alpha']);
    expect(rows.map((r) => r.pinned), [true, false]);
  });

  test('a folded project hides its workspaces but keeps their ids', () {
    final rows = buildTreeRows([argus], const TreeView(folded: {'A:p1'}));
    expect(rows, hasLength(1));
    expect(rows.single.folded, isTrue);
    expect(rows.single.workspaceIds, ['A:w1', 'A:w2']);
  });

  test('hidden and gone rows are off by default', () {
    final hidden = _p('A:p3', 'secret', hidden: true, ws: [_w('A:w9', '/s')]);
    final goneP = _p('A:p4', 'vanished', gone: true);
    expect(buildTreeRows([argus, hidden, goneP], _all).map((r) => r.label), [
      'argus',
      'argus',
      'registry',
    ]);
    final shown = buildTreeRows([
      argus,
      hidden,
      goneP,
    ], const TreeView(showHidden: true, showGone: true));
    expect(
      shown.map((r) => r.label),
      containsAll(['secret', 'vanished', 'old']),
    );
  });

  test('filter matches project, directory, or branch and unfolds', () {
    final rows = buildTreeRows([
      argus,
      crush,
    ], const TreeView(filter: 'feat/', folded: {'A:p1'}));
    expect(rows.map((r) => r.label), ['argus', 'registry']);
    final byProject = buildTreeRows([
      argus,
      crush,
    ], const TreeView(filter: 'crush'));
    expect(byProject.map((r) => r.label), ['crush', 'crush']);
  });

  test('filter ignores case', () {
    final rows = buildTreeRows([
      argus,
      crush,
    ], const TreeView(filter: 'REGISTRY'));
    expect(rows.map((r) => r.label), ['argus', 'registry']);
  });

  test('blank filter acts as no filter', () {
    expect(
      buildTreeRows([argus, crush], const TreeView(filter: '   ')).length,
      buildTreeRows([argus, crush], _all).length,
    );
  });

  test('plain projects have no detail line and git errors show', () {
    final plain = _p(
      'A:p5',
      'notes',
      kind: 'plain',
      error: 'boom',
      ws: [_w('A:w5', '/notes')],
    );
    final rows = buildTreeRows([plain], _all);
    expect(rows.first.gitError, 'boom');
    expect(rows.last.detail, isNull);
  });

  test(
    'activity counts live and waiting sessions, skipping dead and offline',
    () {
      final sessions = [
        _s('1', SessionStatus.awaitingInput, ws: 'A:w1'),
        _s('2', SessionStatus.working, ws: 'A:w1'),
        _s('3', SessionStatus.dead, ws: 'A:w1'),
        _s('4', SessionStatus.working, ws: 'A:w2', offline: true),
        _s('5', SessionStatus.idle),
      ];
      final act = workspaceActivity(sessions);
      expect(act['A:w1']!.live, 2);
      expect(act['A:w1']!.waiting, 1);
      expect(act.containsKey('A:w2'), isFalse);
      final home = homeActivity(sessions);
      expect(home.live, 3);
      expect(home.waiting, 1);
      final sum = sumActivity(act, ['A:w1', 'A:w2']);
      expect(sum.live, 2);
    },
  );
}
