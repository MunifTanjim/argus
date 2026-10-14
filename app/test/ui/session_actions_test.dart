import 'dart:convert';

import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/control.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/ui/session_sections_list.dart';
import 'package:argus/ui/sidebar_sessions_list.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_session_repository.dart';

Session _s(String id, {String frontend = 'tmux'}) => Session.fromJson(jsonDecode(jsonEncode({
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
    })) as Map<String, dynamic>);

class _Repo extends FakeSessionRepository {
  final renamed = <(String, String)>[];
  final killed = <String>[];
  bool applied = true;

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
}) =>
    tester.pumpWidget(ProviderScope(
      overrides: [
        projectsProvider.overrideWith(_Projects.new),
        sessionRepositoryProvider.overrideWithValue(repo),
        outdatedNodeIdsProvider.overrideWithValue(outdated),
      ],
      child: MaterialApp(
        home: Scaffold(body: SessionSectionsList(sessions: [s])),
      ),
    ));

Future<void> _renameTo(WidgetTester tester, String name) async {
  await tester.tap(find.text('Rename'));
  await tester.pumpAndSettle();
  await tester.enterText(find.byType(TextField), name);
  await tester.pump();
  await tester.tap(find.text('Save'));
  await tester.pumpAndSettle();
}

void main() {
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

  testWidgets('a rename the agent takes later says it was sent', (tester) async {
    final repo = _Repo()..applied = false;
    await _pumpList(tester, repo, _s('A:s1'));
    await tester.longPress(find.text('Old'));
    await tester.pumpAndSettle();
    await _renameTo(tester, 'New');
    expect(find.text('rename to New sent'), findsOneWidget);
  });

  testWidgets('a long press on a session card kills it after confirming',
      (tester) async {
    final repo = _Repo();
    await _pumpList(tester, repo, _s('A:s1'));
    await tester.longPress(find.text('Old'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Kill'));
    await tester.pumpAndSettle();
    expect(find.text('Kill Session?'), findsOneWidget);
    await tester.tap(find.widgetWithText(TextButton, 'Kill'));
    await tester.pumpAndSettle();
    expect(repo.killed, ['A:s1']);
  });

  testWidgets('a session that cannot be killed offers only rename',
      (tester) async {
    await _pumpList(tester, _Repo(), _s('A:s1', frontend: 'vscode'));
    await tester.longPress(find.text('Old'));
    await tester.pumpAndSettle();
    expect(find.text('Rename'), findsOneWidget);
    expect(find.text('Kill'), findsNothing);
  });

  testWidgets('an outdated node offers an upgrade note instead of actions',
      (tester) async {
    await _pumpList(tester, _Repo(), _s('A:s1'), outdated: {'A'});
    await tester.longPress(find.text('Old'));
    await tester.pumpAndSettle();
    expect(find.text('Rename'), findsNothing);
    expect(find.text('Kill'), findsNothing);
    expect(find.text('Upgrade argus on dev'), findsOneWidget);
  });

  testWidgets('the sidebar details sheet renames the session', (tester) async {
    final repo = _Repo();
    await tester.pumpWidget(ProviderScope(
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
    ));
    await tester.pump();
    await tester.longPress(find.text('Old'));
    await tester.pumpAndSettle();
    expect(find.text('Kill'), findsOneWidget);
    await _renameTo(tester, 'New');
    expect(repo.renamed, [('A:s1', 'New')]);
  });
}
