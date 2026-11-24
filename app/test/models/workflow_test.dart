import 'dart:async';

import 'package:argus/data/workflow_repository.dart';
import 'package:argus/core/result.dart';
import 'package:argus/models/entry.dart';
import 'package:argus/models/session.dart';
import 'package:argus/models/workflow.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/transport/rpc_client.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('parses a workflow entry', () {
    final e = Entry.fromJson({
      'id': 'e1',
      'kind': 'workflow',
      'toolName': 'Workflow',
      'toolId': 't1',
      'inputPreview': 'review',
      'workflow': {
        'runId': 'wf_1',
        'name': 'review',
        'status': 'running',
        'remote': true,
        'launchedAt': '2026-11-23T09:00:00Z',
      },
    });
    expect(e.kind, EntryKind.workflow);
    expect(e.toolName, 'Workflow');
    expect(e.workflow!.runId, 'wf_1');
    expect(e.workflow!.name, 'review');
    expect(e.workflow!.status, 'running');
    expect(e.workflow!.remote, isTrue);
    expect(e.workflow!.launchedAt, '2026-11-23T09:00:00Z');
  });

  test('workflow ref defaults and unknown kinds', () {
    final e = Entry.fromJson({
      'id': 'e1',
      'kind': 'workflow',
      'workflow': {'status': 'done'},
    });
    expect(e.workflow!.remote, isFalse);
    expect(e.workflow!.launchedAt, isNull);
    expect(
      Entry.fromJson({'id': 'x', 'kind': 'bogus'}).kind,
      EntryKind.unknown,
    );
    expect(Entry.fromJson({'id': 'x', 'kind': 'tool'}).workflow, isNull);
  });

  test('parses a workflow view', () {
    final v = WorkflowView.fromJson({
      'runId': 'wf_1',
      'name': 'review',
      'status': 'done',
      'remote': false,
      'launchedAt': '2026-11-23T09:00:00Z',
      'phases': [
        {
          'title': 'Plan',
          'agents': [
            {'id': 'a1', 'label': 'planner', 'state': 'done', 'hasTrace': true},
            {'id': 'a2'},
          ],
        },
        {'title': 'Empty'},
      ],
    });
    expect(v.ref.runId, 'wf_1');
    expect(v.ref.status, 'done');
    expect(v.phases, hasLength(2));
    expect(v.phases.first.title, 'Plan');
    expect(v.phases.first.agents.first.label, 'planner');
    expect(v.phases.first.agents.first.hasTrace, isTrue);
    expect(v.phases.first.agents.last.state, '');
    expect(v.phases.last.agents, isEmpty);
  });

  test('parses running workflows on a session', () {
    final base = {
      'id': 'n:%1',
      'agent': 'claude',
      'status': 'working',
      'source': 'hooked',
      'tmux': {'pane_id': '%1'},
    };
    expect(Session.fromJson(base).workflows, isEmpty);
    final s = Session.fromJson({
      ...base,
      'workflows': [
        {
          'run_id': 'wf_1',
          'name': 'review',
          'done': 2,
          'total': 5,
          'since': '2026-11-23T09:00:00Z',
        },
        {'run_id': 'wf_2'},
      ],
    });
    expect(s.workflows, hasLength(2));
    final w = s.workflows.first;
    expect(w.runId, 'wf_1');
    expect(w.name, 'review');
    expect(w.done, 2);
    expect(w.total, 5);
    expect(w.since, DateTime.utc(2026, 11, 23, 9));
    expect(s.workflows.last.total, 0);
  });

  group('WorkflowRepositoryRemote', () {
    test('live() emits sessions.workflow with session_id and run_id', () async {
      final frames = <String>[];
      final incoming = StreamController<RpcMessage>();
      final client = RpcClient(
        incoming: incoming.stream,
        sendFrame: frames.add,
      );
      // ignore: unawaited_futures
      WorkflowRepositoryRemote(() => client).live('n:s1', 'wf_1', agentId: 'a');
      await Future<void>.delayed(Duration.zero);
      expect(frames.single, contains('"method":"sessions.workflow"'));
      expect(frames.single, contains('"session_id":"n:s1"'));
      expect(frames.single, contains('"run_id":"wf_1"'));
      expect(frames.single, contains('"agent_id":"a"'));
    });

    test(
      'history() emits sessions.historyWorkflow and requires node_id',
      () async {
        final frames = <String>[];
        final incoming = StreamController<RpcMessage>();
        final client = RpcClient(
          incoming: incoming.stream,
          sendFrame: frames.add,
        );
        final repo = WorkflowRepositoryRemote(() => client);
        final noNode = await repo.history(transcriptPath: '/t', runId: 'wf_1');
        expect(noNode, isA<Error<WorkflowView>>());
        expect(frames, isEmpty);
        // ignore: unawaited_futures
        repo.history(
          nodeId: 'n',
          agent: 'claude',
          transcriptPath: '/t.jsonl',
          runId: 'wf_1',
        );
        await Future<void>.delayed(Duration.zero);
        expect(frames.single, contains('"method":"sessions.historyWorkflow"'));
        expect(frames.single, contains('"node_id":"n"'));
        expect(frames.single, contains('"transcript_path":"/t.jsonl"'));
        expect(frames.single, contains('"run_id":"wf_1"'));
      },
    );

    test('live() without a client is an error', () async {
      final r = await WorkflowRepositoryRemote(() => null).live('n:s1', 'wf');
      expect(r, isA<Error<WorkflowView>>());
    });
  });
}
