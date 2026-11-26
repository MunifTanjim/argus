import 'dart:convert';

import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/control.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/ui/details_header.dart';
import 'package:argus/ui/session_sections_list.dart';
import 'package:argus/ui/sidebar_sessions_list.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_session_repository.dart';

Session _s(
  String id, {
  String frontend = 'tmux',
  Map<String, dynamic> extra = const {},
}) => Session.fromJson(
  jsonDecode(
        jsonEncode({
          'id': id,
          'frontend': frontend,
          'agent': 'claude',
          'name': 'Old',
          'status': 'idle',
          'source': 'hooked',
          'node_id': 'A',
          'node_label': 'dev',
          'tmux': {
            'server': 'argus',
            'pane_id': '%1',
            'session_name': 's',
            'window_index': 0,
            'current_path': '/p',
          },
          ...extra,
        }),
      )
      as Map<String, dynamic>,
);

class _Repo extends FakeSessionRepository {
  final renamed = <(String, String)>[];
  final killed = <String>[];
  bool applied = true;
  Result<int> descendants = const Result.ok(0);

  @override
  Future<Result<bool>> rename(String sessionId, String name) async {
    renamed.add((sessionId, name));
    return Result.ok(applied);
  }

  @override
  Future<Result<void>> kill(String sessionId) async {
    killed.add(sessionId);
    return const Result.ok(null);
  }

  @override
  Future<Result<int>> descendantCount(String sessionId) async => descendants;
}

class _Projects extends ProjectsNotifier {
  @override
  ProjectsState build() => const ProjectsState(projects: [], loaded: true);
}

class _Sessions extends SessionsNotifier {
  _Sessions(this._seed);
  final List<Session> _seed;
  @override
  Map<String, Session> build() => {for (final s in _seed) s.id: s};
}

Future<void> _pumpList(
  WidgetTester tester,
  _Repo repo,
  Session s, {
  Set<String> outdated = const {},
}) => tester.pumpWidget(
  ProviderScope(
    overrides: [
      projectsProvider.overrideWith(_Projects.new),
      sessionRepositoryProvider.overrideWithValue(repo),
      outdatedNodeIdsProvider.overrideWithValue(outdated),
    ],
    child: MaterialApp(
      home: Scaffold(body: SessionSectionsList(sessions: [s])),
    ),
  ),
);

Future<void> _renameTo(WidgetTester tester, String name) async {
  await tester.tap(find.text('Rename'));
  await tester.pumpAndSettle();
  await tester.enterText(find.byType(TextField), name);
  await tester.pump();
  await tester.tap(find.text('Save'));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('a long press on a session card opens the session sheet', (
    tester,
  ) async {
    await _pumpList(tester, _Repo(), _s('A:s1'));
    await tester.longPress(find.text('Old'));
    await tester.pumpAndSettle();
    expect(find.byType(DetailsHeader), findsOneWidget);
    expect(
      find.descendant(
        of: find.byType(DetailsActionBar),
        matching: find.text('Kill'),
      ),
      findsOneWidget,
    );
    expect(find.text('A:s1'), findsOneWidget);
  });

  testWidgets('a long press on a session card renames it', (tester) async {
    final repo = _Repo();
    await _pumpList(tester, repo, _s('A:s1'));
    await tester.longPress(find.text('Old'));
    await tester.pumpAndSettle();
    expect(find.text('Rename'), findsOneWidget);
    expect(find.text('Kill'), findsOneWidget);

    await _renameTo(tester, 'New');
    expect(repo.renamed, [('A:s1', 'New')]);
    expect(find.text('renamed to New'), findsOneWidget);
  });

  testWidgets('a rename the agent takes later says it was sent', (
    tester,
  ) async {
    final repo = _Repo()..applied = false;
    await _pumpList(tester, repo, _s('A:s1'));
    await tester.longPress(find.text('Old'));
    await tester.pumpAndSettle();
    await _renameTo(tester, 'New');
    expect(find.text('rename to New sent'), findsOneWidget);
  });

  testWidgets('a long press on a session card kills it after confirming', (
    tester,
  ) async {
    final repo = _Repo();
    await _pumpList(tester, repo, _s('A:s1'));
    await tester.longPress(find.text('Old'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kill'));
    await tester.pumpAndSettle();
    expect(find.text('Kill Session?'), findsOneWidget);
    await tester.tap(find.widgetWithText(FilledButton, 'Kill'));
    await tester.pumpAndSettle();
    expect(repo.killed, ['A:s1']);
  });

  for (final (count, text) in [
    (
      const Result<int>.ok(2),
      'Its 2 child sessions are killed too. This cannot be undone!',
    ),
    (
      const Result<int>.ok(1),
      'Its 1 child session is killed too. This cannot be undone!',
    ),
    (
      Result<int>.error(Exception('method not found')),
      'Child sessions: unknown. This cannot be undone!',
    ),
  ]) {
    testWidgets('the kill confirmation says: $text', (tester) async {
      final repo = _Repo()..descendants = count;
      await _pumpList(tester, repo, _s('A:s1'));
      await tester.longPress(find.text('Old'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('Kill'));
      await tester.pumpAndSettle();
      expect(find.text(text), findsOneWidget);
      await tester.tap(find.widgetWithText(FilledButton, 'Kill'));
      await tester.pumpAndSettle();
      expect(repo.killed, ['A:s1']);
    });
  }

  testWidgets('a session that cannot be killed offers only rename', (
    tester,
  ) async {
    await _pumpList(tester, _Repo(), _s('A:s1', frontend: 'vscode'));
    await tester.longPress(find.text('Old'));
    await tester.pumpAndSettle();
    expect(find.text('Rename'), findsOneWidget);
    expect(find.text('Kill'), findsNothing);
  });

  testWidgets('an outdated node offers an upgrade note instead of actions', (
    tester,
  ) async {
    await _pumpList(tester, _Repo(), _s('A:s1'), outdated: {'A'});
    await tester.longPress(find.text('Old'));
    await tester.pumpAndSettle();
    expect(find.text('Rename'), findsNothing);
    expect(find.text('Kill'), findsNothing);
    expect(find.text('Upgrade argus on dev'), findsOneWidget);
  });

  testWidgets('the sidebar details sheet renames the session', (tester) async {
    final repo = _Repo();
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          projectsProvider.overrideWith(_Projects.new),
          sessionsProvider.overrideWith(() => _Sessions([_s('A:s1')])),
          gatewayProvider.overrideWithValue(null),
          sessionRepositoryProvider.overrideWithValue(repo),
          outdatedNodeIdsProvider.overrideWithValue(const {}),
        ],
        child: MaterialApp(
          home: Scaffold(body: SidebarSessionsList(onOpen: () {})),
        ),
      ),
    );
    await tester.pump();
    await tester.longPress(find.text('Old'));
    await tester.pumpAndSettle();
    expect(find.text('Kill'), findsOneWidget);
    await _renameTo(tester, 'New');
    expect(repo.renamed, [('A:s1', 'New')]);
  });

  testWidgets('the sidebar sheet keeps its actions in view on a short screen', (
    tester,
  ) async {
    await tester.binding.setSurfaceSize(const Size(400, 520));
    addTearDown(() => tester.binding.setSurfaceSize(null));
    await _openSidebarSheet(tester, _long);
    expect(tester.takeException(), isNull);
    expect(find.text('Rename').hitTestable(), findsOneWidget);
    expect(find.text('Kill').hitTestable(), findsOneWidget);
    expect(
      tester.getCenter(find.text('Kill')).dx,
      lessThan(tester.getCenter(find.text('Rename')).dx),
    );
    expect(
      find.text('a task that is long enough to wrap onto two lines'),
      findsOneWidget,
    );
  });

  testWidgets(
    'a tap on the sidebar sheet id row copies the id and keeps the sheet',
    (tester) async {
      final copied = <String>[];
      tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        SystemChannels.platform,
        (call) async {
          if (call.method == 'Clipboard.setData') {
            copied.add((call.arguments as Map)['text'] as String);
          }
          return null;
        },
      );
      addTearDown(
        () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
          SystemChannels.platform,
          null,
        ),
      );
      await _openSidebarSheet(tester, _long);
      await tester.ensureVisible(find.text('A:s1'));
      await tester.pumpAndSettle();
      await tester.tap(find.text('A:s1'));
      await tester.pump();
      expect(copied, ['A:s1']);
      expect(find.text('Rename'), findsOneWidget);
      expect(find.text('Copied'), findsOneWidget);
      await tester.pumpAndSettle(const Duration(seconds: 2));
      expect(find.text('Copied'), findsNothing);
      expect(find.text('A:s1'), findsOneWidget);
    },
  );

  testWidgets('an outdated node shows the upgrade note in the sidebar sheet', (
    tester,
  ) async {
    await _openSidebarSheet(tester, _long, outdated: const {'A'});
    expect(find.text('Upgrade argus on dev'), findsOneWidget);
    expect(find.text('Rename'), findsNothing);
    expect(find.text('Kill'), findsNothing);
  });
}

final _long = _s(
  'A:s1',
  extra: {
    'cwd': '/src/a/very/long/path/that/wraps/onto/more/than/one/line/here',
    'branch': 'feat/a-long-branch-name-for-the-sheet',
    'summary': {'task': 'a task that is long enough to wrap onto two lines'},
  },
);

Future<void> _openSidebarSheet(
  WidgetTester tester,
  Session s, {
  Set<String> outdated = const {},
}) async {
  await tester.pumpWidget(
    ProviderScope(
      overrides: [
        projectsProvider.overrideWith(_Projects.new),
        sessionsProvider.overrideWith(() => _Sessions([s])),
        gatewayProvider.overrideWithValue(null),
        sessionRepositoryProvider.overrideWithValue(_Repo()),
        outdatedNodeIdsProvider.overrideWithValue(outdated),
      ],
      child: MaterialApp(
        home: Scaffold(body: SidebarSessionsList(onOpen: () {})),
      ),
    ),
  );
  await tester.pump();
  await tester.longPress(find.text('Old'));
  await tester.pumpAndSettle();
}
