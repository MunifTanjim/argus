import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/session.dart';
import '../models/tasks.dart';
import '../state/tasks.dart';
import 'responsive.dart';
import 'theme.dart';
import 'top_bar.dart';

const _mono = TextStyle(fontFamily: monoFamily, fontSize: 13);

/// A live session's Claude Code task list (TaskCreate/TaskUpdate), grouped by
/// status. The session detail beneath refetches it on tasks.changed pushes.
class SessionTasksScreen extends ConsumerStatefulWidget {
  const SessionTasksScreen({super.key, required this.session});

  final Session session;

  @override
  ConsumerState<SessionTasksScreen> createState() => _SessionTasksScreenState();
}

class _SessionTasksScreenState extends ConsumerState<SessionTasksScreen> {
  Future<void> _refresh() {
    ref.invalidate(tasksProvider(widget.session.id));
    return ref.read(tasksProvider(widget.session.id).future);
  }

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(tasksProvider(widget.session.id));
    return Scaffold(
      appBar: TopBar(
        title: const Text('Tasks'),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: 'Refresh',
            onPressed: _refresh,
          ),
        ],
      ),
      body: SafeArea(
        top: false,
        child: RefreshIndicator(
          onRefresh: _refresh,
          child: async.when(
            loading: () => const Center(child: CircularProgressIndicator()),
            error: (e, _) => _messageList('Could not load tasks:\n$e'),
            data: (tasks) => _body(context, tasks),
          ),
        ),
      ),
    );
  }

  Widget _messageList(String text) => ListView(
        children: [
          const SizedBox(height: 120),
          Center(
              child:
                  Text(text, style: TextStyle(color: context.tokens.textDim))),
        ],
      );

  Widget _body(BuildContext context, List<Task> tasks) {
    final inProgress = <Task>[];
    final pending = <Task>[];
    final completed = <Task>[];
    for (final t in tasks) {
      switch (t.status) {
        case TaskStatus.inProgress:
          inProgress.add(t);
        case TaskStatus.completed:
          completed.add(t);
        case TaskStatus.pending:
        case TaskStatus.unknown:
          pending.add(t);
      }
    }

    return CenteredBody(
      child: ListView(
        padding: const EdgeInsets.all(12),
        children: [
          if (tasks.isEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 100),
              child: Center(
                child: Text('No tasks for this session.',
                    style: TextStyle(color: context.tokens.textDim)),
              ),
            ),
          ..._section('In Progress', inProgress),
          ..._section('Pending', pending),
          ..._section('Completed', completed),
        ],
      ),
    );
  }

  List<Widget> _section(String title, List<Task> tasks) {
    if (tasks.isEmpty) return const [];
    return [
      _sectionHeader('${title.toUpperCase()} (${tasks.length})'),
      for (final t in tasks) _TaskRow(task: t),
      const SizedBox(height: 8),
    ];
  }

  Widget _sectionHeader(String text) => Padding(
        padding: const EdgeInsets.only(bottom: 8, top: 4),
        child: Text(
          '▌ $text',
          style: TextStyle(
            fontFamily: monoFamily,
            fontSize: 12,
            fontWeight: FontWeight.w700,
            color: context.tokens.textDim,
          ),
        ),
      );
}

class _TaskRow extends StatelessWidget {
  const _TaskRow({required this.task});
  final Task task;

  @override
  Widget build(BuildContext context) {
    final done = task.status == TaskStatus.completed;
    final title = task.status == TaskStatus.inProgress &&
            task.activeForm.isNotEmpty
        ? task.activeForm
        : task.subject;

    return Container(
      margin: const EdgeInsets.only(bottom: 6),
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 10),
      decoration: BoxDecoration(
        color: context.tokens.surface,
        border: Border.all(color: context.tokens.line),
        borderRadius: BorderRadius.circular(context.tokens.radius),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(top: 1, right: 8),
            child: Icon(_statusIcon(task.status),
                size: 16, color: _statusColor(context.tokens, task.status)),
          ),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  '#${task.id}  $title',
                  style: _mono.copyWith(
                    color: done ? context.tokens.textDim : context.tokens.text,
                    decoration:
                        done ? TextDecoration.lineThrough : TextDecoration.none,
                  ),
                ),
                if (task.description.isNotEmpty) ...[
                  const SizedBox(height: 4),
                  Text(
                    task.description,
                    style: _mono.copyWith(color: context.tokens.textDim, fontSize: 11),
                    maxLines: 4,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
                if (task.blockedBy.isNotEmpty) ...[
                  const SizedBox(height: 4),
                  Text(
                    'blocked by ${task.blockedBy.map((b) => '#$b').join(', ')}',
                    style: _mono.copyWith(
                        color: context.tokens.error, fontSize: 11),
                  ),
                ],
              ],
            ),
          ),
        ],
      ),
    );
  }

  static IconData _statusIcon(TaskStatus s) {
    switch (s) {
      case TaskStatus.completed:
        return Icons.check_circle;
      case TaskStatus.inProgress:
        return Icons.autorenew;
      case TaskStatus.pending:
        return Icons.radio_button_unchecked;
      case TaskStatus.unknown:
        return Icons.help_outline;
    }
  }

  static Color _statusColor(ArgusTokens t, TaskStatus s) {
    switch (s) {
      case TaskStatus.completed:
        return t.taskCompleted;
      case TaskStatus.inProgress:
        return t.taskInProgress;
      case TaskStatus.pending:
      case TaskStatus.unknown:
        return t.textDim;
    }
  }
}
