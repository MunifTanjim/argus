import 'package:argus/state/workspace.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

void main() {
  test(
    'calls each workspace method with its params and parses results',
    () async {
      final client = FakeGatewayClient(
        (m, p) async => switch (m) {
          'workspace.changedFiles' => {
            'files': [
              {'path': 'a.go', 'change': 'modified', 'staged': true},
            ],
          },
          'workspace.diff' => {'path': 'a.go', 'diff': '@@ -1 +1 @@\n-a\n+b\n'},
          'workspace.commits' => {
            'commits': [
              {
                'sha': 's1',
                'short': 's1',
                'subject': 'x',
                'author': 'me',
                'unix_sec': 1,
              },
            ],
          },
          'workspace.commitFiles' => {'files': []},
          'workspace.listDir' => {
            'entries': [
              {'name': 'lib', 'path': 'lib', 'is_dir': true},
            ],
          },
          'workspace.readFile' => {'path': 'a.go', 'content': 'package a'},
          _ => null,
        },
      );
      final api = WorkspaceApi(() => client);

      expect(
        (await api.changedFiles('A:w1', against: 'target')).single.path,
        'a.go',
      );
      expect(client.calls.last.$1, 'workspace.changedFiles');
      expect(client.calls.last.$2, {
        'workspace_id': 'A:w1',
        'against': 'target',
      });

      expect((await api.diff('A:w1', 'a.go', rev: 's1')).diff, contains('+b'));
      expect(client.calls.last.$1, 'workspace.diff');
      expect(client.calls.last.$2, {
        'workspace_id': 'A:w1',
        'path': 'a.go',
        'rev': 's1',
      });

      expect((await api.commits('A:w1')).commits.single.sha, 's1');
      expect(client.calls.last.$1, 'workspace.commits');
      expect(client.calls.last.$2, {'workspace_id': 'A:w1'});

      expect(await api.commitFiles('A:w1', 's1'), isEmpty);
      expect(client.calls.last.$1, 'workspace.commitFiles');
      expect(client.calls.last.$2, {'workspace_id': 'A:w1', 'sha': 's1'});

      expect((await api.listDir('A:w1', '')).entries.single.isDir, isTrue);
      expect(client.calls.last.$1, 'workspace.listDir');
      expect(client.calls.last.$2, {'workspace_id': 'A:w1', 'path': ''});

      expect((await api.readFile('A:w1', 'a.go')).content, 'package a');
      expect(client.calls.last.$1, 'workspace.readFile');
      expect(client.calls.last.$2, {'workspace_id': 'A:w1', 'path': 'a.go'});
    },
  );

  test('uncommitted changedFiles omits against', () async {
    final client = FakeGatewayClient((m, p) async => {'files': []});
    await WorkspaceApi(() => client).changedFiles('A:w1');
    expect(client.calls.last.$2, {'workspace_id': 'A:w1'});
  });

  test('no client throws', () {
    expect(WorkspaceApi(() => null).commits('A:w1'), throwsStateError);
  });
}
