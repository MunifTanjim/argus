import 'dart:convert';

import 'package:argus/models/changes.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/changes.dart';
import 'package:argus/ui/changed_files_screen.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

Session _session() => Session.fromJson(
      jsonDecode(jsonEncode({
        'id': 'n1:s1',
        'agent': 'claude',
        'status': 'active',
        'source': 'hooked',
        'repo': 'my-repo',
        'tmux': {
          'server': 'argus',
          'pane_id': '%1',
          'session_name': 's',
          'window_index': 0,
          'current_path': '/p',
        },
      })) as Map<String, dynamic>,
    );

void main() {
  testWidgets('renders the unpushed commits section and taps into a commit',
      (tester) async {
    await tester.pumpWidget(
      ProviderScope(
        overrides: [
          changedFilesProvider(_session().id)
              .overrideWith((ref) async => <ChangedFile>[]),
          commitsProvider(_session().id).overrideWith(
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
          commitFilesProvider((_session().id, 'deadbeef'))
              .overrideWith((ref) async => <ChangedFile>[]),
        ],
        child: MaterialApp(home: ChangedFilesScreen(session: _session())),
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
          changedFilesProvider(_session().id)
              .overrideWith((ref) async => <ChangedFile>[]),
          commitsProvider(_session().id).overrideWith(
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
        child: MaterialApp(home: ChangedFilesScreen(session: _session())),
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
