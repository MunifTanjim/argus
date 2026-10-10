import 'dart:convert';

import 'package:argus/models/enums.dart';
import 'package:argus/models/project.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/state/projects.dart';
import 'package:argus/ui/agent_badge.dart';
import 'package:argus/ui/nerd_icon.dart';
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

Future<void> _pump(WidgetTester tester, GroupBy by, List<Session> sessions) =>
    tester.pumpWidget(ProviderScope(
      overrides: [projectsProvider.overrideWith(_SeededProjects.new)],
      child: MaterialApp(
        home: Scaffold(
          body: SessionSectionsList(sessions: sessions, groupBy: by),
        ),
      ),
    ));

Finder _inHeaders(Finder f) =>
    find.descendant(of: find.byType(SectionLabel), matching: f);

void main() {
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
    expect(colors, [agentColor('claude'), AppColors.dim]);
  });

  testWidgets('status headers lead with the status glyph', (tester) async {
    await _pump(tester, GroupBy.status, [_s('s1', status: 'working')]);
    expect(_inHeaders(find.text(statusGlyph(SessionStatus.working))),
        findsOneWidget);
  });
}
