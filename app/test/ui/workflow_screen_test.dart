import 'package:argus/core/result.dart';
import 'package:argus/data/workflow_repository.dart';
import 'package:argus/models/entry.dart';
import 'package:argus/models/session.dart';
import 'package:argus/models/workflow.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/state/tool_detail.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/ui/entry_row.dart';
import 'package:argus/ui/running_workflows_sheet.dart';
import 'package:argus/ui/subagent_trace_screen.dart';
import 'package:argus/ui/workflow_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeRepo implements WorkflowRepository {
  _FakeRepo(this.view);
  WorkflowView view;
  Object? fail;
  final calls = <String>[];

  @override
  Future<Result<WorkflowView>> live(
    String sessionId,
    String runId, {
    String? agentId,
  }) async {
    calls.add('live $sessionId $runId ${agentId ?? ''}');
    if (fail != null) return Result.error(fail!);
    return Result.ok(view);
  }

  @override
  Future<Result<WorkflowView>> history({
    String? nodeId,
    String? agent,
    required String transcriptPath,
    required String runId,
    String? agentId,
  }) async {
    calls.add('history $nodeId $transcriptPath $runId ${agentId ?? ''}');
    return Result.ok(view);
  }
}

WorkflowView _view({String status = 'running', List<WorkflowPhase>? phases}) =>
    WorkflowView(
      ref: WorkflowRef(runId: 'wf1', name: 'review', status: status),
      phases:
          phases ??
          const [
            WorkflowPhase(
              title: 'Scan',
              agents: [
                WorkflowAgent(
                  id: 'a1',
                  label: 'scan api',
                  state: 'done',
                  hasTrace: true,
                ),
                WorkflowAgent(id: 'a2', label: 'scan ui', state: 'running'),
              ],
            ),
            WorkflowPhase(
              title: '',
              agents: [
                WorkflowAgent(id: 'a3', label: 'merge', state: 'queued'),
              ],
            ),
          ],
    );

Session _session({List<Map<String, dynamic>> workflows = const []}) =>
    Session.fromJson({
      'id': 's',
      'agent': 'claude',
      'status': 'working',
      'source': 'hooked',
      'tmux': {'pane_id': '%1'},
      'workflows': workflows,
    });

Map<String, dynamic> _wf(int done, int total) => {
  'run_id': 'wf1',
  'name': 'review',
  'done': done,
  'total': total,
};

Future<ProviderContainer> _pumpScreen(
  WidgetTester t,
  _FakeRepo repo, {
  ToolDetailRef parentRef = const ToolDetailRef.live('s', agent: 'claude'),
  Session? session,
}) async {
  final container = ProviderContainer(
    overrides: [
      workflowRepositoryProvider.overrideWithValue(repo),
      gatewayProvider.overrideWithValue(null),
    ],
  );
  addTearDown(container.dispose);
  container.read(sessionsProvider.notifier).put(session ?? _session());
  await t.pumpWidget(
    UncontrolledProviderScope(
      container: container,
      child: MaterialApp(
        home: WorkflowScreen(parentRef: parentRef, runId: 'wf1'),
      ),
    ),
  );
  await t.pump();
  return container;
}

Future<void> _pumpRow(WidgetTester t, Entry e) => t.pumpWidget(
  ProviderScope(
    overrides: [
      workflowRepositoryProvider.overrideWithValue(_FakeRepo(_view())),
      gatewayProvider.overrideWithValue(null),
    ],
    child: MaterialApp(
      home: Scaffold(
        body: EntryRow(
          detailRef: const ToolDetailRef.live('s', agent: 'claude'),
          entry: e,
        ),
      ),
    ),
  ),
);

Entry _entry({String runId = 'wf1', bool remote = false}) => Entry(
  id: 'e',
  kind: EntryKind.workflow,
  workflow: WorkflowRef(
    runId: runId,
    name: 'review',
    status: 'running',
    remote: remote,
  ),
);

void main() {
  group('row', () {
    testWidgets('shows name and status, no count', (t) async {
      await _pumpRow(t, _entry());
      expect(find.text('Workflow review'), findsOneWidget);
      expect(find.text('running'), findsOneWidget);
      expect(find.textContaining('/'), findsNothing);
    });

    testWidgets('a drillable row opens the workflow page', (t) async {
      await _pumpRow(t, _entry());
      await t.tap(find.text('Workflow review'));
      await t.pumpAndSettle();
      final screen = t.widget<WorkflowScreen>(find.byType(WorkflowScreen));
      expect(screen.runId, 'wf1');
      expect(screen.parentRef.sessionId, 's');
    });

    testWidgets('a remote row is not tappable', (t) async {
      await _pumpRow(t, _entry(remote: true));
      expect(find.byType(InkWell), findsNothing);
      await t.tap(find.text('Workflow review'));
      await t.pumpAndSettle();
      expect(find.byType(WorkflowScreen), findsNothing);
    });

    testWidgets('a row without a run id is not tappable', (t) async {
      await _pumpRow(t, _entry(runId: ''));
      expect(find.byType(InkWell), findsNothing);
    });
  });

  group('page', () {
    testWidgets('renders the header, phases and agents', (t) async {
      final repo = _FakeRepo(_view());
      await _pumpScreen(t, repo);
      expect(repo.calls, ['live s wf1 ']);
      expect(find.text('review · running'), findsOneWidget);
      expect(find.text('Scan'), findsOneWidget);
      expect(find.text('Phase 2'), findsOneWidget);
      expect(find.text('scan api'), findsOneWidget);
      expect(find.text('done'), findsOneWidget);
      expect(find.text('scan ui'), findsOneWidget);
      expect(find.text('merge'), findsOneWidget);
      expect(find.text('queued'), findsOneWidget);
    });

    testWidgets('a sole untitled phase gets no header', (t) async {
      final repo = _FakeRepo(
        _view(
          phases: const [
            WorkflowPhase(
              agents: [WorkflowAgent(id: 'a1', label: 'solo', state: 'done')],
            ),
          ],
        ),
      );
      await _pumpScreen(t, repo);
      expect(find.text('solo'), findsOneWidget);
      expect(find.text('Phase 1'), findsNothing);
    });

    testWidgets('tapping an agent with a trace opens it', (t) async {
      await _pumpScreen(t, _FakeRepo(_view()));
      await t.tap(find.text('scan api'));
      await t.pumpAndSettle();
      final screen = t.widget<SubagentTraceScreen>(
        find.byType(SubagentTraceScreen),
      );
      final sub = screen.item.soleSubagent!;
      expect(sub.id, 'a1');
      expect(sub.name, 'scan api');
      expect(sub.status, 'done');
      expect(sub.hasTrace, isTrue);
      expect(screen.parentRef.sessionId, 's');
    });

    testWidgets('an agent without a trace is not tappable', (t) async {
      await _pumpScreen(t, _FakeRepo(_view()));
      await t.tap(find.text('scan ui'));
      await t.pumpAndSettle();
      expect(find.byType(SubagentTraceScreen), findsNothing);
    });

    testWidgets('refetches when the run progresses or ends', (t) async {
      final repo = _FakeRepo(_view());
      final c = await _pumpScreen(
        t,
        repo,
        session: _session(workflows: [_wf(1, 3)]),
      );
      expect(repo.calls, hasLength(1));
      repo.view = _view(status: 'running');
      c.read(sessionsProvider.notifier).put(_session(workflows: [_wf(2, 3)]));
      await t.pump();
      expect(repo.calls, hasLength(2));
      // An unrelated session update does not refetch.
      c.read(sessionsProvider.notifier).put(_session(workflows: [_wf(2, 3)]));
      await t.pump();
      expect(repo.calls, hasLength(2));
      repo.view = _view(status: 'completed');
      c.read(sessionsProvider.notifier).put(_session());
      await t.pump();
      expect(repo.calls, hasLength(3));
      expect(find.text('review · completed'), findsOneWidget);
    });

    testWidgets('a failed refetch reports over the last view', (t) async {
      final repo = _FakeRepo(_view());
      await _pumpScreen(
        t,
        repo,
        parentRef: const ToolDetailRef.live('s', agentId: 'sub1'),
      );
      repo.fail = StateError('gone');
      await t.pump(const Duration(seconds: 2));
      await t.pump();
      expect(find.text('workflow: Bad state: gone'), findsOneWidget);
      expect(find.text('scan api'), findsOneWidget);
      expect(find.text('review · running'), findsOneWidget);
      // The failed poll is not retried.
      await t.pump(const Duration(seconds: 4));
      expect(repo.calls, hasLength(2));
    });

    testWidgets('refetches on reconnect', (t) async {
      final repo = _FakeRepo(_view());
      final c = await _pumpScreen(t, repo);
      c.read(connStateProvider.notifier).state = ConnState.reconnecting;
      await t.pump();
      c.read(connStateProvider.notifier).state = ConnState.connected;
      await t.pump();
      expect(repo.calls, hasLength(2));
    });

    testWidgets('a page opened from a trace polls while running', (t) async {
      final repo = _FakeRepo(_view());
      await _pumpScreen(
        t,
        repo,
        parentRef: const ToolDetailRef.live('s', agentId: 'sub1'),
      );
      expect(repo.calls, ['live s wf1 sub1']);
      await t.pump(const Duration(seconds: 2));
      expect(repo.calls, hasLength(2));
      repo.view = _view(status: 'completed');
      await t.pump(const Duration(seconds: 2));
      expect(repo.calls, hasLength(3));
      await t.pump(const Duration(seconds: 4));
      expect(repo.calls, hasLength(3));
    });

    testWidgets('polling stops when the page closes', (t) async {
      final repo = _FakeRepo(_view());
      await _pumpScreen(
        t,
        repo,
        parentRef: const ToolDetailRef.live('s', agentId: 'sub1'),
      );
      await t.pumpWidget(const SizedBox());
      await t.pump(const Duration(seconds: 4));
      expect(repo.calls, hasLength(1));
    });

    testWidgets('a main-session page does not poll', (t) async {
      final repo = _FakeRepo(_view());
      await _pumpScreen(t, repo);
      await t.pump(const Duration(seconds: 4));
      expect(repo.calls, hasLength(1));
    });

    testWidgets('a history page fetches once from the transcript', (t) async {
      final repo = _FakeRepo(_view());
      await _pumpScreen(
        t,
        repo,
        parentRef: const ToolDetailRef.history(
          nodeId: 'n',
          transcriptPath: '/t.jsonl',
          agentId: 'sub1',
        ),
      );
      expect(repo.calls, ['history n /t.jsonl wf1 sub1']);
      await t.pump(const Duration(seconds: 4));
      expect(repo.calls, hasLength(1));
      expect(find.text('scan api'), findsOneWidget);
    });
  });

  group('running sheet', () {
    testWidgets('rows show name and progress, and open the page', (t) async {
      final repo = _FakeRepo(_view());
      final container = ProviderContainer(
        overrides: [
          workflowRepositoryProvider.overrideWithValue(repo),
          gatewayProvider.overrideWithValue(null),
        ],
      );
      addTearDown(container.dispose);
      container
          .read(sessionsProvider.notifier)
          .put(
            _session(
              workflows: [
                _wf(1, 3),
                {'run_id': 'wf2', 'done': 0, 'total': 2},
              ],
            ),
          );
      await t.pumpWidget(
        UncontrolledProviderScope(
          container: container,
          child: MaterialApp(
            home: Builder(
              builder: (context) => TextButton(
                onPressed: () => showRunningWorkflows(
                  context,
                  sessionId: 's',
                  agent: 'claude',
                ),
                child: const Text('open'),
              ),
            ),
          ),
        ),
      );
      await t.tap(find.text('open'));
      await t.pumpAndSettle();
      expect(find.text('review · 1/3'), findsOneWidget);
      expect(find.text('Workflow · 0/2'), findsOneWidget);
      await t.tap(find.text('review · 1/3'));
      await t.pumpAndSettle();
      final screen = t.widget<WorkflowScreen>(find.byType(WorkflowScreen));
      expect(screen.runId, 'wf1');
      expect(screen.parentRef.sessionId, 's');
      expect(screen.parentRef.agentId, isNull);
      expect(screen.parentRef.agent, 'claude');
    });
  });
}
