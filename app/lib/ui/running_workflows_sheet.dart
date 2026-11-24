import 'package:flutter/material.dart';

import '../state/tool_detail.dart';
import 'running_sheet.dart';
import 'workflow_screen.dart';

Future<void> showRunningWorkflows(
  BuildContext context, {
  required String sessionId,
  required String agent,
}) => showRunningSheet(
  context,
  sessionId: sessionId,
  rows: (s) => [
    for (final w in s.workflows)
      RunningRow(
        title:
            '${(w.name ?? '').isNotEmpty ? w.name : 'Workflow'}'
            ' · ${w.done}/${w.total}',
        since: w.since,
        open: (_) => WorkflowScreen(
          parentRef: ToolDetailRef.live(sessionId, agent: agent),
          runId: w.runId,
        ),
      ),
  ],
);
