import 'package:argus/models/enums.dart';
import 'package:argus/models/project_sources.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/projects_api.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

Session _s(
  String id,
  String ws, {
  SessionStatus status = SessionStatus.working,
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
);

// Records compare a map field by identity, so check the method and the params
// apart.
void _last(FakeGatewayClient c, String method, Object? params) {
  expect(c.calls.last.$1, method);
  expect(c.calls.last.$2, params);
}

void main() {
  test('calls each method with the node contract params', () async {
    final client = FakeGatewayClient(
      (m, p) async => switch (m) {
        'project.branches' => {
          'branches': [
            {'name': 'main'},
          ],
        },
        'project.prs' => {'prs': [], 'truncated': true},
        'project.issues' => {'issues': []},
        'workspace.create' => {'workspace_id': 'A:w9', 'dir': '/x'},
        'workspace.remove' => {'warning': 'teardown failed'},
        'workspace.setupLog' => {'output': 'done'},
        _ => null,
      },
    );
    final api = ProjectsApi(() => client);

    await api.rename('A:p1', 'new');
    _last(client, 'project.rename', {'project_id': 'A:p1', 'name': 'new'});
    await api.setPinned('A:p1', true);
    _last(client, 'project.setPinned', {'project_id': 'A:p1', 'value': true});
    await api.setHidden('A:p1', false);
    _last(client, 'project.setHidden', {'project_id': 'A:p1', 'value': false});
    await api.forget('A:p1');
    _last(client, 'project.forget', {'project_id': 'A:p1'});
    expect((await api.branches('A:p1')).single.name, 'main');
    _last(client, 'project.branches', {'project_id': 'A:p1'});
    expect((await api.prs('A:p1')).truncated, isTrue);
    expect((await api.issues('A:p1')).items, isEmpty);
    final r = await api.create(
      'A:p1',
      const CreateRequest(source: 'new', branch: 'b'),
    );
    expect(r.workspaceId, 'A:w9');
    _last(client, 'workspace.create', {
      'project_id': 'A:p1',
      'source': 'new',
      'branch': 'b',
    });
    expect(await api.remove('A:w2', force: true), 'teardown failed');
    _last(client, 'workspace.remove', {'workspace_id': 'A:w2', 'force': true});
    await api.remove('A:w2');
    _last(client, 'workspace.remove', {'workspace_id': 'A:w2'});
    await api.setTarget('A:w2', 'dev');
    _last(client, 'workspace.setTarget', {
      'workspace_id': 'A:w2',
      'target_branch': 'dev',
    });
    await api.runSetup('A:w2');
    _last(client, 'workspace.runSetup', {'workspace_id': 'A:w2'});
    expect(await api.setupLog('A:w2'), 'done');
  });

  test('actionError shows the RPC message', () {
    expect(
      actionError(const RpcError(-32600, 'workspace has uncommitted changes')),
      'workspace has uncommitted changes',
    );
    expect(actionError(StateError('not connected')), 'not connected');
  });

  test('liveGuard counts live sessions in the given workspaces', () {
    final sessions = [
      _s('s1', 'A:w1'),
      _s('s2', 'A:w1'),
      _s('s3', 'A:w2'),
      _s('s4', 'A:w3', status: SessionStatus.dead),
    ];
    expect(liveGuard('x', sessions, ['A:w3']), isNull);
    expect(
      liveGuard('x', sessions, ['A:w2']),
      'x has 1 live session · kill it first',
    );
    expect(
      liveGuard('argus', sessions, ['A:w1', 'A:w2']),
      'argus has 3 live sessions · kill them first',
    );
  });
}
