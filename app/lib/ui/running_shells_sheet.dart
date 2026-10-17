import 'package:flutter/material.dart';

import '../models/entry.dart';
import '../models/session.dart';
import '../state/tool_detail.dart';
import 'item_detail_screen.dart';
import 'running_sheet.dart';
import 'theme.dart';

Future<void> showRunningShells(
  BuildContext context, {
  required String sessionId,
  required String agent,
}) => showRunningSheet(
  context,
  sessionId: sessionId,
  rows: (s) => [
    for (final sh in s.shells) _row(sh, ToolDetailRef.live(sessionId, agent: agent)),
  ],
);

RunningRow _row(RunningShell sh, ToolDetailRef ref) {
  final desc = sh.desc ?? '';
  final cmd = sh.command ?? '';
  final toolId = sh.toolUseId ?? '';
  return RunningRow(
    title: desc.isNotEmpty ? desc : cmd.isNotEmpty ? cmd : 'Shell',
    subtitle: desc.isNotEmpty && cmd.isNotEmpty ? cmd : null,
    subtitleStyle: mono,
    since: sh.since,
    open: toolId.isEmpty
        ? null
        : (_) => ItemDetailScreen(
              item: Entry(id: sh.id, kind: EntryKind.tool, toolName: 'Bash', toolId: toolId),
              detailRef: ref,
            ),
  );
}
