import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/state/navigation.dart';
import 'package:argus/ui/resume_action.dart';

import '../support/fake_session_repository.dart';

class _ResumePage extends ConsumerWidget {
  const _ResumePage();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Scaffold(
      body: ElevatedButton(
        onPressed: () => resumeSession(
          context,
          ref,
          nodeId: 'home',
          agent: 'claude',
          agentSessionId: 'sess-1',
          cwd: '/home/user/project',
        ),
        child: const Text('resume'),
      ),
    );
  }
}

Widget _harness(SessionRepository repo) => ProviderScope(
      overrides: [sessionRepositoryProvider.overrideWithValue(repo)],
      child: MaterialApp(
        home: Scaffold(
          body: Builder(
            builder: (context) => ElevatedButton(
              onPressed: () => Navigator.of(context).push(
                MaterialPageRoute(builder: (_) => const _ResumePage()),
              ),
              child: const Text('go'),
            ),
          ),
        ),
      ),
    );

/// Pushes the resume page so popUntil-first has something to unwind.
Future<void> _openResumePage(
    WidgetTester tester, SessionRepository repo) async {
  await tester.pumpWidget(_harness(repo));
  await tester.tap(find.text('go'));
  await tester.pumpAndSettle();
  expect(find.text('resume'), findsOneWidget);
}

void main() {
  testWidgets('success pops to the session list and notifies', (tester) async {
    final repo = FakeSessionRepository(
      resumeResult: const Result.ok(ResumeOutcome(sessionId: 'default:%9')),
    );
    await _openResumePage(tester, repo);

    await tester.tap(find.text('resume'));
    await tester.pumpAndSettle();

    expect(find.text('resume'), findsNothing);
    expect(find.text('go'), findsOneWidget);
    expect(find.text('Resuming session…'), findsOneWidget);
  });

  testWidgets('forwards the resume arguments to the repository',
      (tester) async {
    final repo = FakeSessionRepository(
      resumeResult: const Result.ok(ResumeOutcome(sessionId: 'default:%9')),
    );
    await _openResumePage(tester, repo);

    await tester.tap(find.text('resume'));
    await tester.pumpAndSettle();

    expect(repo.lastResumeArgs, {
      'nodeId': 'home',
      'agent': 'claude',
      'agentSessionId': 'sess-1',
      'cwd': '/home/user/project',
    });
  });

  testWidgets('success switches to the Sessions tab', (tester) async {
    final container = ProviderContainer(overrides: [
      sessionRepositoryProvider.overrideWithValue(
        FakeSessionRepository(
          resumeResult: const Result.ok(ResumeOutcome(sessionId: 'default:%9')),
        ),
      ),
    ]);
    addTearDown(container.dispose);
    container.read(homeTabProvider.notifier).state = 1; // start on History

    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: MaterialApp(
        home: Scaffold(
          body: Builder(
            builder: (context) => ElevatedButton(
              onPressed: () => Navigator.of(context).push(
                MaterialPageRoute(builder: (_) => const _ResumePage()),
              ),
              child: const Text('go'),
            ),
          ),
        ),
      ),
    ));
    await tester.tap(find.text('go'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('resume'));
    await tester.pumpAndSettle();

    expect(container.read(homeTabProvider), homeTabSessions);
  });

  testWidgets('failure stays on the page and shows the error', (tester) async {
    final repo = FakeSessionRepository(
      resumeResult: Result.error(Exception('boom')),
    );
    await _openResumePage(tester, repo);

    await tester.tap(find.text('resume'));
    await tester.pumpAndSettle();

    expect(find.text('resume'), findsOneWidget);
    expect(find.textContaining('Failed to resume'), findsOneWidget);
  });
}
