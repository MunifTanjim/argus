import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../state/navigation.dart';
import '../state/push.dart';

/// On success, opens the resumed session's transcript page once it appears in the
/// live list (discovery may lag the resume). The terminal is a separate action.
Future<void> resumeSession(
  BuildContext context,
  WidgetRef ref, {
  String? nodeId,
  required String agent,
  required String agentSessionId,
  required String cwd,
}) async {
  // The user can leave Home while the resume is in flight, which disposes
  // [context]; the session must still open in Home.
  final container = ProviderScope.containerOf(context, listen: false);
  final messenger = ScaffoldMessenger.of(context);
  final result = await ref.read(sessionRepositoryProvider).resume(
        nodeId: nodeId,
        agent: agent,
        agentSessionId: agentSessionId,
        cwd: cwd,
      );
  switch (result) {
    case Ok(:final value):
      showHomeSessions(container);
      messenger.showSnackBar(
        const SnackBar(content: Text('Resuming session…')),
      );
      if (context.mounted) Navigator.of(context).popUntil((r) => r.isFirst);
      if (value.sessionId.isNotEmpty) {
        container.read(pendingOpenSessionProvider.notifier).state =
            PendingOpen(value.sessionId, inHome: true);
      }
    case Error(:final error):
      messenger.showSnackBar(
        SnackBar(content: Text('Failed to resume: $error')),
      );
  }
}
