import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../state/navigation.dart';

/// On success, returns to the live Sessions list, where the resumed session
/// appears (live-jumped or freshly launched) once discovery catches up.
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
    case Ok():
      ref.read(homeTabProvider.notifier).state = homeTabSessions;
      ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Resuming session…')),
      );
      Navigator.of(context).popUntil((r) => r.isFirst);
    case Error(:final error):
      ScaffoldMessenger.of(context).showSnackBar(
        SnackBar(content: Text('Failed to resume: $error')),
      );
  }
}
