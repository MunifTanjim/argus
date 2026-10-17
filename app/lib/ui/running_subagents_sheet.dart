import 'package:flutter/material.dart';

import '../models/entry.dart';
import '../state/tool_detail.dart';
import 'running_sheet.dart';
import 'subagent_trace_screen.dart';

Future<void> showRunningSubagents(
  BuildContext context, {
  required String sessionId,
  required String agent,
}) => showRunningSheet(
  context,
  sessionId: sessionId,
  rows: (s) => [
    for (final a in s.subagents)
      RunningRow(
        title: (a.name ?? '').isNotEmpty
            ? a.name!
            : (a.type ?? '').isNotEmpty
            ? a.type!
            : 'Subagent',
        subtitle: (a.desc ?? '').isNotEmpty ? a.desc : null,
        since: a.since,
        open: (_) => SubagentTraceScreen(
          parentRef: ToolDetailRef.live(sessionId, agent: agent),
          item: Entry(
            id: a.id,
            kind: EntryKind.subagent,
            subagents: [
              Subagent(
                id: a.id,
                type: a.type ?? '',
                name: a.name ?? '',
                desc: a.desc ?? '',
                status: 'running',
                hasTrace: true,
              ),
            ],
          ),
        ),
      ),
  ],
);
