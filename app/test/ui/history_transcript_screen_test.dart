import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/history_repository.dart';
import 'package:argus/models/entry.dart';
import 'package:argus/models/history.dart';
import 'package:argus/state/control.dart';
import 'package:argus/ui/history_transcript_screen.dart';
import 'package:argus/ui/agent_badge.dart';
import 'package:argus/ui/shell_drawer.dart';
import 'package:argus/ui/theme.dart';
import 'package:argus/ui/usage_format.dart';

class _FakeHistoryRepository implements HistoryRepository {
  _FakeHistoryRepository(this._result);
  final Object _result; // List<Entry> or Exception

  @override
  Future<Result<List<Entry>>> transcript({
    String? nodeId,
    required String transcriptPath,
    String? agentId,
    String? agent,
  }) async {
    final r = _result;
    if (r is Exception) return Result.error(r);
    return Result.ok(r as List<Entry>);
  }

  @override
  Future<Result<List<HistoryProject>>> projects() async => const Result.ok([]);

  @override
  Future<Result<HistorySessionPage>> sessions({
    String? nodeId,
    required String projectDir,
    required int limit,
    required int offset,
  }) async =>
      const Result.ok(HistorySessionPage(items: [], hasMore: false));
}

HistorySession _session({
  String? title,
  String? firstMessage,
  bool resumable = false,
  String? nodeId,
  String? nodeLabel,
}) =>
    HistorySession(
      sessionId: 'sess-1',
      nodeId: nodeId,
      nodeLabel: nodeLabel,
      title: title,
      firstMessage: firstMessage,
      transcriptPath: '/path/to/transcript',
      lastActivity: '',
      tokens: 0,
      turnCount: 0,
      durationMs: 0,
      resumable: resumable,
    );

HistoryProject _project({
  required String cwd,
  bool missing = false,
  String? nodeLabel,
}) =>
    HistoryProject(
      nodeLabel: nodeLabel,
      projectDir: '/home/user/project',
      cwd: cwd,
      label: 'Project',
      sessionCount: 1,
      lastActivity: '',
      missing: missing,
    );

Widget _app(
  HistoryRepository repo,
  HistorySession session, {
  HistoryProject? project,
  Set<String> outdatedNodes = const {},
}) =>
    ProviderScope(
      overrides: [
        historyRepositoryProvider.overrideWithValue(repo),
        outdatedNodeIdsProvider.overrideWithValue(outdatedNodes),
      ],
      child: MaterialApp(
        home: HistoryTranscriptScreen(session: session, project: project),
      ),
    );

void main() {
  testWidgets('renders chunk text when transcript loads', (tester) async {
    final repo = _FakeHistoryRepository(const [
      Entry(id: 'c1', kind: EntryKind.user, text: 'hello from history'),
    ]);
    await tester.pumpWidget(_app(repo, _session(title: 'My Session')));
    await tester.pump(); // initState fires, future resolves
    await tester.pump(); // setState re-render
    expect(find.textContaining('hello from history'), findsOneWidget);
  });

  testWidgets('shows error text when transcript call fails', (tester) async {
    final repo = _FakeHistoryRepository(Exception('network error'));
    await tester.pumpWidget(_app(repo, _session()));
    await tester.pump();
    await tester.pump();
    expect(find.textContaining('network error'), findsOneWidget);
  });

  testWidgets('uses title as AppBar title', (tester) async {
    final repo = _FakeHistoryRepository(const <Entry>[]);
    await tester.pumpWidget(_app(repo, _session(title: 'My Title')));
    await tester.pump();
    await tester.pump();
    expect(find.text('My Title'), findsOneWidget);
  });

  testWidgets('falls back to firstMessage when title is null', (tester) async {
    final repo = _FakeHistoryRepository(const <Entry>[]);
    await tester.pumpWidget(
        _app(repo, _session(title: null, firstMessage: 'first msg')));
    await tester.pump();
    await tester.pump();
    expect(find.text('first msg'), findsOneWidget);
  });

  testWidgets('falls back to sessionId when title and firstMessage null',
      (tester) async {
    final repo = _FakeHistoryRepository(const <Entry>[]);
    await tester.pumpWidget(
        _app(repo, _session(title: null, firstMessage: null)));
    await tester.pump();
    await tester.pump();
    expect(find.text('sess-1'), findsOneWidget);
  });

  testWidgets('shows resume button when resumable and cwd is known',
      (tester) async {
    final repo = _FakeHistoryRepository(const <Entry>[]);
    await tester.pumpWidget(_app(repo, _session(resumable: true),
        project: _project(cwd: '/home/user/project')));
    await tester.pump();
    await tester.pump();
    expect(find.byKey(const Key('history-resume-bar')), findsOneWidget);
  });

  testWidgets('hides resume button when cwd is unknown', (tester) async {
    final repo = _FakeHistoryRepository(const <Entry>[]);
    await tester.pumpWidget(_app(repo, _session(resumable: true),
        project: _project(cwd: '')));
    await tester.pump();
    await tester.pump();
    expect(find.byKey(const Key('history-resume-bar')), findsNothing);
  });

  testWidgets('hides resume button when the directory is gone',
      (tester) async {
    final repo = _FakeHistoryRepository(const <Entry>[]);
    await tester.pumpWidget(_app(repo, _session(resumable: true),
        project: _project(cwd: '/home/user/project', missing: true)));
    await tester.pump();
    await tester.pump();
    expect(find.byKey(const Key('history-resume-bar')), findsNothing);
  });

  testWidgets('hides resume button when the node is outdated', (tester) async {
    final repo = _FakeHistoryRepository(const <Entry>[]);
    await tester.pumpWidget(_app(repo, _session(resumable: true, nodeId: 'n-old'),
        project: _project(cwd: '/home/user/project'), outdatedNodes: const {'n-old'}));
    await tester.pump();
    await tester.pump();
    expect(find.byKey(const Key('history-resume-bar')), findsNothing);
  });

  testWidgets('resume bar names the machine and the short folder',
      (tester) async {
    final repo = _FakeHistoryRepository(const <Entry>[]);
    await tester.pumpWidget(_app(
        repo, _session(resumable: true, nodeLabel: 'MacBook'),
        project: _project(cwd: '/Users/me/Dev/github/me/argus')));
    await tester.pump();
    await tester.pump();
    expect(find.text('MacBook · ~/Dev/…/argus'), findsOneWidget);
    expect(find.byIcon(Icons.play_arrow), findsOneWidget);
    expect(
        find.descendant(
            of: find.byKey(const Key('history-resume-bar')),
            matching: find.byIcon(Icons.dns_outlined)),
        findsOneWidget);
  });

  testWidgets('resume bar falls back to the project machine name',
      (tester) async {
    final repo = _FakeHistoryRepository(const <Entry>[]);
    await tester.pumpWidget(_app(
        repo, _session(resumable: true, nodeLabel: ''),
        project: _project(cwd: '/srv/api', nodeLabel: 'devbox')));
    await tester.pump();
    await tester.pump();
    expect(find.text('devbox · /srv/api'), findsOneWidget);
  });

  testWidgets('tapping the title opens the session details', (tester) async {
    final repo = _FakeHistoryRepository(const <Entry>[]);
    await tester.pumpWidget(_app(
        repo,
        HistorySession(
          sessionId: 'sess-1',
          title: 'Fix the flaky auth test',
          firstMessage: 'the auth test fails 1 in 5 runs',
          transcriptPath: '/t',
          lastActivity: '',
          tokens: 48200,
          turnCount: 12,
          durationMs: 34 * 60000,
          modelName: 'opus-4',
          modelColor: '#d3869b',
          agent: 'claude',
          nodeLabel: 'MacBook',
        ),
        project: _project(cwd: '/home/user/project')));
    await tester.pump();
    await tester.pump();

    await tester.tap(find.descendant(
        of: find.byKey(const Key('history-title')),
        matching: find.byType(InkWell)));
    await tester.pumpAndSettle();

    expect(find.text('Project'), findsOneWidget);
    expect(find.text('MacBook'), findsOneWidget);
    expect(find.byIcon(Icons.dns_outlined), findsOneWidget);
    expect(find.text('Claude · opus-4 · 12 turns · 34m · 48.2k tok'),
        findsOneWidget);
    expect(find.textContaining('the auth test fails 1 in 5 runs'),
        findsOneWidget);
  });

  testWidgets('session details color the agent and the model',
      (tester) async {
    final repo = _FakeHistoryRepository(const <Entry>[]);
    await tester.pumpWidget(_app(
        repo,
        const HistorySession(
          sessionId: 'sess-1',
          title: 'T',
          transcriptPath: '/t',
          lastActivity: '',
          tokens: 0,
          turnCount: 2,
          durationMs: 0,
          modelName: 'opus-4',
          modelColor: '#d3869b',
          agent: 'claude',
        ),
        project: _project(cwd: '/home/user/project')));
    await tester.pump();
    await tester.pump();
    await tester.tap(find.descendant(
        of: find.byKey(const Key('history-title')),
        matching: find.byType(InkWell)));
    await tester.pumpAndSettle();

    final context = tester.element(find.byType(HistoryTranscriptScreen));
    final t = context.tokens;
    final meta = tester.widget<Text>(find.text('Claude · opus-4 · 2 turns'));
    final spans = <String, Color?>{};
    meta.textSpan!.visitChildren((span) {
      if (span is TextSpan && span.text != null) {
        spans[span.text!] = span.style?.color;
      }
      return true;
    });
    expect(spans['Claude'], agentColor('claude', t));
    expect(spans['opus-4'], readableOn(hexColor('#d3869b')!, t));
    expect(spans['2 turns'], t.textDim);
  });

  testWidgets('has no sidebar button, even inside the shell', (tester) async {
    final repo = _FakeHistoryRepository(const <Entry>[]);
    await tester.pumpWidget(ProviderScope(
      overrides: [historyRepositoryProvider.overrideWithValue(repo)],
      child: MaterialApp(
        home: ShellDrawerScope(
          openDrawer: () {},
          child: HistoryTranscriptScreen(session: _session()),
        ),
      ),
    ));
    await tester.pump();
    await tester.pump();
    expect(find.byTooltip('Projects'), findsNothing);
  });

  testWidgets('session details put the machine at the right edge',
      (tester) async {
    tester.view.physicalSize = const Size(390, 844);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final repo = _FakeHistoryRepository(const <Entry>[]);
    await tester.pumpWidget(_app(
        repo, _session(title: 'T', nodeLabel: 'MacBook'),
        project: _project(cwd: '/home/user/project')));
    await tester.pump();
    await tester.pump();
    await tester.tap(find.descendant(
        of: find.byKey(const Key('history-title')),
        matching: find.byType(InkWell)));
    await tester.pumpAndSettle();

    final sheet = tester.getRect(find.byType(BottomSheet));
    final machine = tester.getRect(find.text('MacBook'));
    expect(machine.right, closeTo(sheet.right - 16, 0.5));
  });
}
