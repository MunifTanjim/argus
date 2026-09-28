import 'package:argus/models/changes.dart';
import 'package:argus/state/changes.dart';
import 'package:argus/state/workspace.dart';
import 'package:argus/ui/changes_view.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

void main() {
  const src = WorkspaceChangesSource('A:w1', 'target');

  testWidgets('lists workspace files and opens a unified diff', (tester) async {
    final client = FakeGatewayClient(
      (m, p) async => switch (m) {
        'workspace.diff' => {
          'path': 'lib/a.dart',
          'diff': '@@ -1 +1 @@\n-old\n+new\n',
        },
        _ => null,
      },
    );
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          workspaceApiProvider.overrideWithValue(WorkspaceApi(() => client)),
          workspaceChangedFilesProvider(('A:w1', 'target')).overrideWith(
            (ref) async => const [
              ChangedFile(path: 'lib/a.dart', change: 'modified', staged: true),
            ],
          ),
          workspaceCommitsProvider(
            'A:w1',
          ).overrideWith((ref) async => const CommitList()),
        ],
        child: const MaterialApp(
          home: Scaffold(body: ChangesView(source: src)),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.textContaining('STAGED (1)'), findsOneWidget);
    await tester.tap(find.textContaining('a.dart').first);
    await tester.pumpAndSettle();

    expect(find.textContaining('+ new'), findsOneWidget);
    final (method, params) = client.calls.single;
    expect(method, 'workspace.diff');
    expect(params, {
      'workspace_id': 'A:w1',
      'path': 'lib/a.dart',
      'against': 'target',
    });
  });

  test('sources compare by value', () {
    expect(
      const WorkspaceChangesSource('w', ''),
      const WorkspaceChangesSource('w', ''),
    );
    expect(
      const WorkspaceChangesSource('w', ''),
      isNot(const WorkspaceChangesSource('w', 'target')),
    );
    expect(const SessionChangesSource('s'), const SessionChangesSource('s'));
  });
}
