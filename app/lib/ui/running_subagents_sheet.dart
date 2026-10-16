import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/entry.dart';
import '../models/session.dart';
import '../state/sessions.dart';
import '../state/tool_detail.dart';
import 'session_composer.dart';
import 'subagent_trace_screen.dart';
import 'theme.dart';

/// A picked subagent's live trace opens on [context]'s navigator, not the
/// sheet's.
Future<void> showRunningSubagents(
  BuildContext context, {
  required String sessionId,
  required String agent,
}) {
  final shell = Navigator.of(context);
  return showModalBottomSheet<void>(
    context: context,
    useRootNavigator: true,
    builder: (_) => _RunningSubagentsSheet(
      sessionId: sessionId,
      agent: agent,
      shell: shell,
    ),
  );
}

class _RunningSubagentsSheet extends ConsumerStatefulWidget {
  const _RunningSubagentsSheet({
    required this.sessionId,
    required this.agent,
    required this.shell,
  });

  final String sessionId;
  final String agent;
  final NavigatorState shell;

  @override
  ConsumerState<_RunningSubagentsSheet> createState() =>
      _RunningSubagentsSheetState();
}

class _RunningSubagentsSheetState
    extends ConsumerState<_RunningSubagentsSheet> {
  late final Timer _tick;

  @override
  void initState() {
    super.initState();
    _tick = Timer.periodic(const Duration(seconds: 1), (_) {
      if (mounted) setState(() {});
    });
  }

  @override
  void dispose() {
    _tick.cancel();
    super.dispose();
  }

  void _open(RunningSubagent a) {
    final item = Entry(
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
    );
    Navigator.of(context).pop();
    widget.shell.push(
      MaterialPageRoute(
        builder: (_) => SubagentTraceScreen(
          parentRef: ToolDetailRef.live(widget.sessionId, agent: widget.agent),
          item: item,
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    ref.listen(
      sessionsProvider.select(
        (m) => m[widget.sessionId]?.subagents.isEmpty ?? true,
      ),
      (_, empty) {
        // Not while closing: a pop then would take the screen above or below.
        if (empty && (ModalRoute.of(context)?.isCurrent ?? false)) {
          Navigator.of(context).pop();
        }
      },
    );
    final subagents =
        ref.watch(
          sessionsProvider.select((m) => m[widget.sessionId]?.subagents),
        ) ??
        const <RunningSubagent>[];
    final now = DateTime.now();
    return SafeArea(
      child: ListView(
        shrinkWrap: true,
        padding: const EdgeInsets.only(top: 8, bottom: 8),
        children: [
          for (final a in subagents)
            ListTile(
              title: Text(
                (a.name ?? '').isNotEmpty
                    ? a.name!
                    : (a.type ?? '').isNotEmpty
                    ? a.type!
                    : 'Subagent',
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
              ),
              subtitle: (a.desc ?? '').isNotEmpty
                  ? Text(a.desc!, maxLines: 2, overflow: TextOverflow.ellipsis)
                  : null,
              trailing: a.since == null
                  ? null
                  : Text(
                      formatElapsed(now.difference(a.since!)),
                      style: mono.copyWith(color: AppColors.dim),
                    ),
              onTap: () => _open(a),
            ),
        ],
      ),
    );
  }
}
