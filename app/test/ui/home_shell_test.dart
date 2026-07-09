import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/enums.dart';
import 'package:argus/models/session.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/push.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/ui/home_shell.dart';
import 'package:argus/ui/session_detail_screen.dart';

Session _session(String id) => Session(
      id: id,
      agent: 'claude',
      tmux: const TmuxLocation(
        server: TmuxServerKind.default_,
        paneId: '',
        sessionName: '',
        windowIndex: 0,
        currentPath: '',
      ),
      status: SessionStatus.awaitingInput,
      source: SessionSource.discovered,
    );

void main() {
  testWidgets('switches to Settings tab and shows Disconnect', (tester) async {
    await tester.pumpWidget(ProviderScope(
      overrides: [gatewayProvider.overrideWithValue(null)],
      child: const MaterialApp(home: HomeShell()),
    ));
    await tester.pump();

    expect(find.text('Sessions'), findsWidgets);
    await tester.tap(find.text('Settings'));
    await tester.pumpAndSettle();
    expect(find.text('Disconnect'), findsOneWidget);
  });

  testWidgets('deep-links to a session pending before mount', (tester) async {
    final container = ProviderContainer(
      overrides: [gatewayProvider.overrideWithValue(null)],
    );
    addTearDown(container.dispose);
    // The tap arrives (and sets the pending session) before HomeShell mounts,
    // and the session list is already populated.
    container.read(sessionsProvider.notifier).replaceAll([_session('s1')]);
    container.read(pendingPushSessionProvider.notifier).state = 's1';

    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: HomeShell()),
    ));
    // SessionDetailScreen shows a perpetual spinner without a connection, so
    // settle is impossible; pump past the route transition instead.
    await tester.pump();
    await tester.pump(const Duration(seconds: 1));

    expect(find.byType(SessionDetailScreen), findsOneWidget);
    expect(container.read(pendingPushSessionProvider), isNull);
  });

  testWidgets('opens a pending session once the list is fetched', (tester) async {
    final container = ProviderContainer(
      overrides: [gatewayProvider.overrideWithValue(null)],
    );
    addTearDown(container.dispose);
    // Cold start: the tap sets a pending session, but the list has not arrived.
    container.read(pendingPushSessionProvider.notifier).state = 's1';

    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: const MaterialApp(home: HomeShell()),
    ));
    await tester.pump();

    // Nothing to open yet; the request must be kept, not discarded.
    expect(find.byType(SessionDetailScreen), findsNothing);
    expect(container.read(pendingPushSessionProvider), 's1');

    // The session list is fetched; the pending session opens now.
    container.read(sessionsProvider.notifier).replaceAll([_session('s1')]);
    await tester.pump();
    await tester.pump(const Duration(seconds: 1));

    expect(find.byType(SessionDetailScreen), findsOneWidget);
    expect(container.read(pendingPushSessionProvider), isNull);
  });
}
