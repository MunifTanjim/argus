import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/terminals.dart';
import 'terminal_tile.dart';

class WorkspaceTerminalsTab extends ConsumerWidget {
  const WorkspaceTerminalsTab({super.key, required this.workspaceId});

  final String workspaceId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final terms = ref
        .watch(terminalsProvider)
        .terminals
        .where((t) => t.workspaceId == workspaceId);
    return TerminalListBody(
      children: [for (final t in terms) TerminalTile(terminal: t)],
    );
  }
}
