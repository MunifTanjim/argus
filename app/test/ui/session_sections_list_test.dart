import 'dart:convert';

import 'package:argus/models/enums.dart';
import 'package:argus/models/project.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/appearance.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/ui/agent_badge.dart';
import 'package:argus/ui/card_layout.dart';
import 'package:argus/ui/nerd_icon.dart';
import 'package:argus/ui/session_card.dart';
import 'package:argus/ui/node_header.dart';
import 'package:argus/ui/session_sections_list.dart';
import 'package:argus/ui/status_style.dart';
import 'package:argus/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

Session _s(String id, {String agent = 'claude', String status = 'idle', String? ws}) =>
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
      'workspace_id': ?ws,
    })) as Map<String, dynamic>);

const _projects = [
  ProjectNode(id: 'p1', name: 'argus', workspaces: [
    WorkspaceNode(id: 'w1', dir: '/src/argus', isMain: true),
    WorkspaceNode(id: 'w2', dir: '/src/argus-feat'),
  ]),
];

class _SeededProjects extends ProjectsNotifier {
  @override
  ProjectsState build() => const ProjectsState(projects: _projects, loaded: true);
}

Future<void> _pump(WidgetTester tester, GroupBy by, List<Session> sessions,
        {bool loading = false, AppearancePrefs? prefs}) =>
    tester.pumpWidget(ProviderScope(
      overrides: [
        projectsProvider.overrideWith(_SeededProjects.new),
        sessionsLoadingProvider.overrideWith((ref) => loading),
      ],
      child: MaterialApp(
        theme: prefs == null ? null : buildArgusTheme(prefs: prefs),
        home: Scaffold(
          body: SessionSectionsList(sessions: sessions, groupBy: by),
        ),
      ),
    ));

Finder _inHeaders(Finder f) =>
    find.descendant(of: find.byType(SectionLabel), matching: f);

void main() {
  testWidgets('an empty list shows a spinner while sessions load',
      (tester) async {
    await _pump(tester, GroupBy.host, const [], loading: true);
    expect(find.byType(CircularProgressIndicator), findsOneWidget);
    expect(find.text('No sessions.'), findsNothing);
  });

  testWidgets('an empty list shows the empty text once loaded',
      (tester) async {
    await _pump(tester, GroupBy.host, const []);
    expect(find.byType(CircularProgressIndicator), findsNothing);
    expect(find.text('No sessions.'), findsOneWidget);
  });

  testWidgets('a non-empty list ignores loading', (tester) async {
    await _pump(tester, GroupBy.host, [_s('s1')], loading: true);
    expect(find.byType(CircularProgressIndicator), findsNothing);
  });

  testWidgets('project headers lead with a project icon', (tester) async {
    await _pump(tester, GroupBy.project, [_s('s1', ws: 'w1')]);
    expect(_inHeaders(find.byType(RepoIcon)), findsOneWidget);
  });

  testWidgets('workspace headers: folder for main, branch for a worktree',
      (tester) async {
    await _pump(tester, GroupBy.workspace, [
      _s('s1', ws: 'w1'),
      _s('s2', ws: 'w2'),
    ]);
    expect(_inHeaders(find.byIcon(Icons.folder_outlined)), findsOneWidget);
    expect(_inHeaders(find.byType(GitBranchIcon)), findsOneWidget);
  });

  testWidgets('agent headers take the agent color; other is dimmed',
      (tester) async {
    await _pump(tester, GroupBy.agent, [
      _s('s1', agent: 'claude'),
      _s('s2', agent: ''),
    ]);
    final colors = tester
        .widgetList<Icon>(_inHeaders(find.byIcon(Icons.smart_toy_outlined)))
        .map((i) => i.color)
        .toList();
    expect(colors, [
      agentColor('claude', ArgusTokens.fallback),
      ArgusTokens.fallback.textDim,
    ]);
  });

  testWidgets('status headers lead with the status glyph', (tester) async {
    await _pump(tester, GroupBy.status, [_s('s1', status: 'working')]);
    expect(_inHeaders(find.text(statusGlyph(SessionStatus.working))),
        findsOneWidget);
  });

  testWidgets('section header is uppercase', (tester) async {
    await _pump(tester, GroupBy.host, [_s('s1', status: 'awaiting_input')]);
    expect(find.text('NEEDS YOU'), findsOneWidget);
  });

  double rowGap(WidgetTester tester) =>
      tester.getRect(find.byType(SessionCard).at(1)).top -
      tester.getRect(find.byType(SessionCard).at(0)).bottom;

  Finder rowDividers() => find.descendant(
      of: find.byType(SessionCard), matching: find.byType(Divider));

  for (final style in [StatusStyle.stripe, StatusStyle.dot]) {
    testWidgets('comfortable ${style.name} rows have a gap and no divider',
        (tester) async {
      await _pump(tester, GroupBy.host, [_s('mac:%1'), _s('mac:%2')],
          prefs: AppearancePrefs(
              statusStyle: style, density: AppDensity.comfortable));
      expect(rowGap(tester), 8);
      expect(rowDividers(), findsNothing);
    });

    testWidgets('compact ${style.name} rows touch, split by a divider',
        (tester) async {
      await _pump(tester, GroupBy.host, [_s('mac:%1'), _s('mac:%2')],
          prefs: AppearancePrefs(statusStyle: style));
      expect(rowGap(tester), 0);
      expect(rowDividers(), findsNWidgets(2));
    });
  }

  testWidgets('filled cards keep their gap in both densities', (tester) async {
    for (final density in AppDensity.values) {
      await _pump(tester, GroupBy.host, [_s('mac:%1'), _s('mac:%2')],
          prefs: AppearancePrefs(
              statusStyle: StatusStyle.filled, density: density));
      expect(rowGap(tester), 8, reason: density.name);
    }
  });

  Session fixture(String id, String repo, {String status = 'working',
          Map<String, dynamic>? interaction}) =>
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
        'repo': repo,
        'summary': {
          'model_name': 'Opus',
          'has_context': true,
          'context_pct': 40,
          'task': 'task of $repo',
        },
        'interaction': ?interaction,
      })) as Map<String, dynamic>);

  for (final style in StatusStyle.values) {
    testWidgets('${style.name}: headers at the card edge, body lines start left',
        (tester) async {
      tester.view.physicalSize = const Size(800, 1600);
      tester.view.devicePixelRatio = 1;
      addTearDown(tester.view.reset);
      await _pump(
          tester,
          GroupBy.status,
          [
            fixture('mac:%1', 'blocked-repo',
                status: 'awaiting_input',
                interaction: {
                  'kind': 'permission',
                  'tool_name': 'Bash',
                  'tool_input': '{"command":"ls"}',
                  'message': 'run it?',
                  'request_id': 'r1',
                }),
            fixture('mac:%2', 'work-repo'),
          ],
          prefs: AppearancePrefs(statusStyle: style));
      double x(Finder f) => tester.getTopLeft(f).dx;
      final title = x(find.text('work-repo'));
      // Body lines start at the card's left content edge, under the mark.
      final body = title - kLeadIndent;
      expect(x(find.text('task of work-repo')), body, reason: 'task');
      expect(x(find.textContaining('40% ctx')), body, reason: 'meta');
      expect(x(find.text('blocked-repo')), title, reason: 'blocked title');
      expect(x(find.text('run it?')), body, reason: 'blocked message');
      // Headers start at the cards' left edge.
      final cardLeft = x(find.byType(SessionCard));
      final blockedHeader = find.descendant(
          of: find.byType(NeedsYouHeader), matching: find.text('BLOCKED'));
      expect(x(blockedHeader), cardLeft + kLeadIndent,
          reason: 'blocked header');
      expect(x(find.text('WORKING').last), cardLeft + kLeadIndent,
          reason: 'status header');
    });
  }
}
