import 'package:argus/models/changes.dart';
import 'package:argus/state/changes.dart';
import 'package:argus/state/workspace.dart';
import 'package:argus/ui/changes_view.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

const _source = WorkspaceChangesSource('A:w1', '');

void main() {
  testWidgets('renders the unpushed commits section and taps into a commit',
      (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          workspaceChangedFilesProvider(('A:w1', ''))
              .overrideWith((ref) async => <ChangedFile>[]),
          workspaceCommitsProvider('A:w1').overrideWith(
            (ref) async => const CommitList(
              unpushed: true,
              commits: [
                Commit(
                  sha: 'deadbeef',
                  short: 'deadbee',
                  subject: 'add commit browser',
                  author: 'Munif',
                  unixSec: 1700000000,
                ),
              ],
            ),
          ),
          workspaceCommitFilesProvider(('A:w1', 'deadbeef'))
              .overrideWith((ref) async => <ChangedFile>[]),
        ],
        child: const MaterialApp(
          home: Scaffold(body: ChangesView(source: _source)),
        ),
      ),
    );
    await tester.pumpAndSettle();

    expect(find.textContaining('UNPUSHED'), findsOneWidget);
    expect(find.text('deadbee'), findsOneWidget);
    expect(find.text('add commit browser'), findsOneWidget);

    await tester.tap(find.text('add commit browser'));
    await tester.pumpAndSettle();

    expect(find.textContaining('deadbee  add commit browser'), findsOneWidget);
    expect(find.text('No files in this commit.'), findsOneWidget);
  });

  testWidgets('tapping the hash copies the full SHA instead of navigating',
      (tester) async {
    final clipboard = <String>[];
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      SystemChannels.platform,
      (call) async {
        if (call.method == 'Clipboard.setData') {
          clipboard.add((call.arguments as Map)['text'] as String);
        }
        return null;
      },
    );

    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          workspaceChangedFilesProvider(('A:w1', ''))
              .overrideWith((ref) async => <ChangedFile>[]),
          workspaceCommitsProvider('A:w1').overrideWith(
            (ref) async => const CommitList(
              unpushed: true,
              commits: [
                Commit(
                  sha: 'deadbeefcafe',
                  short: 'deadbee',
                  subject: 'add commit browser',
                  author: 'Munif',
                  unixSec: 1700000000,
                ),
              ],
            ),
          ),
        ],
        child: const MaterialApp(
          home: Scaffold(body: ChangesView(source: _source)),
        ),
      ),
    );
    await tester.pumpAndSettle();

    await tester.tap(find.byIcon(Icons.copy));
    await tester.pumpAndSettle();

    // Copied the full SHA, and stayed on the list (no navigation to detail).
    expect(clipboard, ['deadbeefcafe']);
    expect(find.text('add commit browser'), findsOneWidget);
    expect(find.text('No files in this commit.'), findsNothing);
  });
}
