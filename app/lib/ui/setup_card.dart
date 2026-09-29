import 'package:flutter/material.dart';

import '../models/project.dart';
import '../state/setup_text.dart';
import 'theme.dart';

/// The TUI setup block: shows only while setup runs or after it failed.
class SetupCard extends StatelessWidget {
  const SetupCard({
    super.key,
    required this.project,
    required this.workspace,
    required this.onRunAgain,
    required this.onFullLog,
  });

  final ProjectNode project;
  final WorkspaceNode workspace;
  final VoidCallback onRunAgain;
  final VoidCallback onFullLog;

  @override
  Widget build(BuildContext context) {
    final run = workspace.setup;
    final head = setupHeadline(run);
    if (run == null || head == null) return const SizedBox.shrink();
    final failed = run.state == 'failed';
    return Card(
      margin: const EdgeInsets.fromLTRB(12, 8, 12, 0),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(12, 10, 12, 4),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              head,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(
                fontWeight: FontWeight.w600,
                color: failed ? AppColors.error : AppColors.secondary,
              ),
            ),
            for (final l in setupTail(run))
              Text(
                l,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: mono.copyWith(color: AppColors.dim),
              ),
            Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                if (project.setupScript.isNotEmpty)
                  TextButton(
                    onPressed: onRunAgain,
                    child: const Text('Run again'),
                  ),
                TextButton(onPressed: onFullLog, child: const Text('Full log')),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
