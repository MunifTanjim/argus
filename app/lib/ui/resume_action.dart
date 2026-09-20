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
  final result = await ref.read(sessionRepositoryProvider).resume(
        nodeId: nodeId,
        agent: agent,
        agentSessionId: agentSessionId,
        cwd: cwd,
      );
  if (!context.mounted) return;
  switch (result) {
    case Ok(:final value):
      ref.read(homeTabProvider.notifier).state = homeTabSessions;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Resuming session…')),
      );
      Navigator.of(context).popUntil((r) => r.isFirst);
      if (value.sessionId.isNotEmpty) {
        ref.read(pendingOpenSessionProvider.notifier).state = value.sessionId;
      }
    case Error(:final error):
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Failed to resume: $error')),
      );
  }
}
