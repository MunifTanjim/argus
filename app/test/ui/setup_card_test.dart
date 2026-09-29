import 'package:argus/models/project.dart';
import 'package:argus/ui/setup_card.dart';
import 'package:argus/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

Future<void> _pump(
  WidgetTester tester,
  SetupRun? run, {
  String script = 'make deps',
  VoidCallback? onRunAgain,
  VoidCallback? onFullLog,
}) => tester.pumpWidget(
  MaterialApp(
    home: Scaffold(
      body: SetupCard(
        project: ProjectNode(id: 'p', name: 'n', setupScript: script),
        workspace: WorkspaceNode(id: 'w', dir: '/x', setup: run),
        onRunAgain: onRunAgain ?? () {},
        onFullLog: onFullLog ?? () {},
      ),
    ),
  ),
);

void main() {
  testWidgets('no card for ok or no run', (tester) async {
    await _pump(tester, null);
    expect(find.byType(Card), findsNothing);
    await _pump(tester, const SetupRun(state: 'ok'));
    expect(find.byType(Card), findsNothing);
  });

  testWidgets('failed: red headline, tail, and working buttons', (
    tester,
  ) async {
    var again = 0, log = 0;
    await _pump(
      tester,
      const SetupRun(
        state: 'failed',
        command: 'make deps',
        exitCode: 2,
        outputTail: 'a\nb\n',
      ),
      onRunAgain: () => again++,
      onFullLog: () => log++,
    );
    final head = tester.widget<Text>(
      find.text('Setup failed (exit 2) · make deps'),
    );
    expect(head.style?.color, AppColors.error);
    expect(find.text('a'), findsOneWidget);
    expect(find.text('b'), findsOneWidget);
    await tester.tap(find.text('Run again'));
    await tester.tap(find.text('Full log'));
    expect((again, log), (1, 1));
  });

  testWidgets('running without a setup script hides Run again', (tester) async {
    await _pump(tester, const SetupRun(state: 'running'), script: '');
    expect(find.text('Setup running'), findsOneWidget);
    expect(find.text('Run again'), findsNothing);
    expect(find.text('Full log'), findsOneWidget);
  });
}
