import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/enums.dart';
import '../models/session.dart';
import '../models/tasks.dart';
import '../state/navigation.dart';
import '../state/tasks.dart';
import '../state/workspace.dart';
import 'running_shells_sheet.dart';
import 'running_subagents_sheet.dart';
import 'session_tasks_screen.dart';
import 'theme.dart';
import 'workspace_screen.dart';

/// The session's tasks, changed files, running subagents, and running shells,
/// as chips above the composer. A chip shows only while its count is not zero,
/// and opens its screen on tap.
class SessionChips extends ConsumerStatefulWidget {
  const SessionChips({
    super.key,
    required this.session,
    this.workspaceId,
    this.onHeight,
    this.bottom = 0,
  });

  final Session session;

  /// The session's workspace, when it is in the tree; null shows no changes.
  final String? workspaceId;

  /// Called with the row's height after a layout that changes it; 0 when no
  /// chip shows. The transcript under the row makes this much room.
  final ValueChanged<double>? onHeight;

  /// Space under the row: some when nothing sits below it, so the chips clear
  /// the screen edge.
  final double bottom;

  @override
  ConsumerState<SessionChips> createState() => _SessionChipsState();
}

class _SessionChipsState extends ConsumerState<SessionChips> {
  final _rowKey = GlobalKey();
  double? _reported;

  void _report() {
    final report = widget.onHeight;
    if (report == null || !mounted) return;
    final box = _rowKey.currentContext?.findRenderObject() as RenderBox?;
    final h = box != null && box.hasSize ? box.size.height : 0.0;
    if (h == _reported) return;
    _reported = h;
    report(h);
  }

  // The changes list is counted against the base the user last picked on the
  // changes screen, so the count matches the list a tap opens.
  (String, String)? get _changesKey {
    final ws = widget.workspaceId;
    return ws == null ? null : (ws, ref.read(changesAgainstProvider(ws)));
  }

  @override
  void didUpdateWidget(SessionChips old) {
    super.didUpdateWidget(old);
    // No event says when files change; a finished turn is when they likely
    // did.
    final key = _changesKey;
    if (key != null &&
        old.session.status == SessionStatus.working &&
        widget.session.status != SessionStatus.working) {
      // After this frame: a provider must not change while widgets build.
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) ref.invalidate(workspaceChangedFilesProvider(key));
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    final s = widget.session;
    final ws = widget.workspaceId;
    final tasks = ref.watch(tasksProvider(s.id)).value ?? const <Task>[];
    final files = ws == null
        ? 0
        : ref
                .watch(workspaceChangedFilesProvider(
                    (ws, ref.watch(changesAgainstProvider(ws)))))
                .value
                ?.length ??
            0;
    final done = tasks.where((t) => t.status == TaskStatus.completed).length;
    final chips = [
      if (tasks.isNotEmpty)
        _Chip(
          key: const Key('chip-tasks'),
          icon: Icons.checklist,
          label: '$done/${tasks.length}',
          onTap: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => SessionTasksScreen(session: s))),
        ),
      if (ws != null && files > 0)
        _Chip(
          key: const Key('chip-changes'),
          icon: Icons.difference,
          label: files == 1 ? '1 file' : '$files files',
          onTap: () => Navigator.of(context).push(MaterialPageRoute(
              builder: (_) => WorkspaceChangesScreen(workspaceId: ws))),
        ),
      if (s.subagents.isNotEmpty)
        _Chip(
          key: const Key('chip-subagents'),
          icon: Icons.smart_toy_outlined,
          label: '${s.subagents.length}',
          onTap: () => showRunningSubagents(context,
              sessionId: s.id, agent: s.agent),
        ),
      if (s.shells.isNotEmpty)
        _Chip(
          key: const Key('chip-shells'),
          icon: Icons.terminal,
          label: '${s.shells.length}',
          onTap: () =>
              showRunningShells(context, sessionId: s.id, agent: s.agent),
        ),
    ];
    if (widget.onHeight != null) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _report());
    }
    if (chips.isEmpty) return const SizedBox.shrink();
    return SingleChildScrollView(
      key: _rowKey,
      scrollDirection: Axis.horizontal,
      // The row spans the width; touches beside the chips reach the feed.
      hitTestBehavior: HitTestBehavior.deferToChild,
      padding: EdgeInsets.fromLTRB(12, 6, 12, widget.bottom),
      child: Row(
        children: [
          for (final (i, c) in chips.indexed) ...[
            if (i > 0) const SizedBox(width: 6),
            c,
          ],
        ],
      ),
    );
  }
}

class _Chip extends StatelessWidget {
  const _Chip({
    super.key,
    required this.icon,
    required this.label,
    required this.onTap,
  });

  final IconData icon;
  final String label;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final shape = RoundedRectangleBorder(
      borderRadius: BorderRadius.circular(12),
      side: BorderSide(color: t.line),
    );
    return Material(
      // Opaque: the row floats over the transcript, and text behind a chip
      // must not show through it.
      color: t.canvas,
      shape: shape,
      child: InkWell(
        customBorder: shape,
        onTap: onTap,
        child: ConstrainedBox(
          constraints: const BoxConstraints(minHeight: 32),
          child: Padding(
            padding: const EdgeInsets.symmetric(horizontal: 10),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(icon, size: 14, color: t.textDim),
                const SizedBox(width: 4),
                Text(label, style: mono.copyWith(color: t.textDim, fontSize: 11)),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
