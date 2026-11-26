import 'package:argus/models/changes.dart';
import 'package:argus/models/session.dart';
import 'package:argus/models/tasks.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/state/tasks.dart';
import 'package:argus/state/workspace.dart';
import 'package:argus/ui/session_chips.dart';
import 'package:argus/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const _id = 'n:%1';
const _ws = 'A:w2';

Session _s({
  String status = 'idle',
  List<Map<String, dynamic>>? subagents,
  List<Map<String, dynamic>>? shells,
}) =>
    Session.fromJson({
      'id': _id,
      'agent': 'claude',
      'status': status,
      'source': 'hooked',
      'frontend': 'tmux',
      'tmux': {'pane_id': '%1'},
      'subagents': ?subagents,
      'shells': ?shells,
    });

Task _task(String id, TaskStatus status) =>
    Task(id: id, subject: 'task $id', status: status);

List<ChangedFile> _files(int n) => [
      for (var i = 0; i < n; i++)
        ChangedFile(path: 'f$i.dart', change: 'modified'),
    ];

const _subagents = [
  {'id': 'a1', 'type': 'Explore', 'desc': 'find it'},
];
const _shells = [
  {'id': 'b1', 'tool_use_id': 't1', 'command': 'make test'},
];

ProviderContainer _container({
  List<Task> tasks = const [],
  List<ChangedFile> Function()? files,
}) {
  final c = ProviderContainer(overrides: [
    tasksProvider(_id).overrideWith((ref) async => tasks),
    workspaceChangedFilesProvider((_ws, ''))
        .overrideWith((ref) async => (files ?? () => const <ChangedFile>[])()),
  ]);
  addTearDown(c.dispose);
  return c;
}

Future<void> _pump(
  WidgetTester t,
  ProviderContainer c,
  Session s, {
  String? workspaceId = _ws,
}) async {
  c.read(sessionsProvider.notifier).put(s);
  await t.pumpWidget(UncontrolledProviderScope(
    container: c,
    child: MaterialApp(
      home: Scaffold(
        body: Align(
          alignment: Alignment.bottomLeft,
          child: SessionChips(session: s, workspaceId: workspaceId),
        ),
      ),
    ),
  ));
  await t.pumpAndSettle();
}

const _tasksKey = Key('chip-tasks');
const _changesKey = Key('chip-changes');
const _subagentsKey = Key('chip-subagents');
const _shellsKey = Key('chip-shells');

void main() {
  testWidgets('no counts show no chips and take no height', (t) async {
    await _pump(t, _container(), _s());
    expect(find.byKey(_tasksKey), findsNothing);
    expect(find.byKey(_changesKey), findsNothing);
    expect(t.getSize(find.byType(SessionChips)).height, 0);
  });

  testWidgets('tasks chip shows done over total', (t) async {
    final c = _container(tasks: [
      _task('1', TaskStatus.completed),
      _task('2', TaskStatus.pending),
      _task('3', TaskStatus.inProgress),
    ]);
    await _pump(t, c, _s());
    expect(
        find.descendant(of: find.byKey(_tasksKey), matching: find.text('1/3')),
        findsOneWidget);
  });

  testWidgets('changes chip counts files, singular and plural', (t) async {
    await _pump(t, _container(files: () => _files(1)), _s());
    expect(find.text('1 file'), findsOneWidget);
    await _pump(t, _container(files: () => _files(5)), _s());
    expect(find.text('5 files'), findsOneWidget);
  });

  testWidgets('no workspace shows no changes chip', (t) async {
    await _pump(t, _container(files: () => _files(2)), _s(), workspaceId: null);
    expect(find.byKey(_changesKey), findsNothing);
  });

  testWidgets('chips keep the order tasks, changes, subagents, shells',
      (t) async {
    final c = _container(
      tasks: [_task('1', TaskStatus.pending)],
      files: () => _files(2),
    );
    await _pump(t, c, _s(subagents: _subagents, shells: _shells));
    final xs = [_tasksKey, _changesKey, _subagentsKey, _shellsKey]
        .map((k) => t.getTopLeft(find.byKey(k)).dx)
        .toList();
    expect(xs, [...xs]..sort());
  });

  testWidgets('tapping the subagents chip opens the sheet', (t) async {
    await _pump(t, _container(), _s(subagents: _subagents));
    await t.tap(find.byKey(_subagentsKey));
    await t.pumpAndSettle();
    expect(find.text('Explore'), findsOneWidget);
  });

  testWidgets('tapping the shells chip opens the sheet', (t) async {
    await _pump(t, _container(), _s(shells: _shells));
    await t.tap(find.byKey(_shellsKey));
    await t.pumpAndSettle();
    expect(find.text('make test'), findsOneWidget);
  });

  testWidgets('the changes count reloads when work ends', (t) async {
    var calls = 0;
    final c = _container(files: () {
      calls++;
      return _files(1);
    });
    await _pump(t, c, _s(status: 'working'));
    expect(calls, 1);
    await _pump(t, c, _s(status: 'idle'));
    expect(calls, 2);
  });

  testWidgets('a narrow screen scrolls the chips without overflow', (t) async {
    t.view.physicalSize = const Size(320, 800);
    t.view.devicePixelRatio = 1;
    addTearDown(t.view.reset);
    final c = _container(
      tasks: [_task('1', TaskStatus.pending)],
      files: () => _files(12),
    );
    await _pump(t, c, _s(subagents: _subagents, shells: _shells));
    expect(t.takeException(), isNull);
    expect(find.byType(SingleChildScrollView), findsOneWidget);
  });

  testWidgets('chips have an opaque canvas fill', (t) async {
    await _pump(t, _container(), _s(shells: _shells));
    final m = t.widget<Material>(find
        .descendant(of: find.byKey(_shellsKey), matching: find.byType(Material))
        .first);
    expect(m.color, ArgusTokens.fallback.canvas);
  });

  testWidgets('reports its height, and zero with no chips', (t) async {
    final heights = <double>[];
    final c = _container();
    c.read(sessionsProvider.notifier).put(_s(shells: _shells));
    Future<void> pump(Session s) async {
      await t.pumpWidget(UncontrolledProviderScope(
        container: c,
        child: MaterialApp(
          home: Scaffold(
            body: Align(
              alignment: Alignment.bottomLeft,
              child: SessionChips(
                  session: s, workspaceId: _ws, onHeight: heights.add),
            ),
          ),
        ),
      ));
      await t.pumpAndSettle();
    }

    await pump(_s(shells: _shells));
    expect(heights.last, t.getSize(find.byType(SessionChips)).height);
    expect(heights.last, greaterThan(0));
    await pump(_s());
    expect(heights.last, 0);
  });
}
