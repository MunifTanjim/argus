import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/session.dart';
import '../models/tasks.dart';
import '../state/gateway.dart';
import '../state/tasks.dart';
import '../transport/connection.dart';
import '../transport/jsonrpc.dart';
import 'responsive.dart';
import 'theme.dart';

const _mono = TextStyle(fontFamily: 'monospace', fontSize: 13);

/// A live session's Claude Code task list (TaskCreate/TaskUpdate), grouped by
/// status. Re-pulls the whole list when the server pushes tasks.changed for
/// this session — detection lives server-side, so the client just refetches.
class SessionTasksScreen extends ConsumerStatefulWidget {
  const SessionTasksScreen({super.key, required this.session});

  final Session session;

  @override
  ConsumerState<SessionTasksScreen> createState() => _SessionTasksScreenState();
}

class _SessionTasksScreenState extends ConsumerState<SessionTasksScreen> {
  StreamSubscription<RpcMessage>? _sub;

  @override
  void initState() {
    super.initState();
    // The transcript subscription owned by the detail screen below keeps the
    // node polling this session, so tasks.changed pushes flow while we're open.
    _bindNotifications();
    // Each reconnect builds a fresh RpcClient with a new notification stream, so
    // rebind to it or live pushes stop after the first blip.
    ref.listenManual(connStateProvider, (_, state) {
      if (state == ConnState.connected) _bindNotifications();
    });
  }

  @override
  void dispose() {
    _sub?.cancel();
    super.dispose();
  }

  void _bindNotifications() {
    _sub?.cancel();
    final client = ref.read(gatewayProvider)?.client;
    _sub = client?.notifications.listen((m) {
      if (m.method != 'tasks.changed') return;
      final params = m.params;
      if (params is Map && params['session_id'] == widget.session.id) {
        _refresh();
      }
    });
  }

  Future<void> _refresh() {
    ref.invalidate(tasksProvider(widget.session.id));
    return ref.read(tasksProvider(widget.session.id).future);
  }

  @override
  Widget build(BuildContext context) {
    final async = ref.watch(tasksProvider(widget.session.id));
    return Scaffold(
      appBar: AppBar(
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
                  Text(text, style: const TextStyle(color: AppColors.dim))),
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
            const Padding(
              padding: EdgeInsets.only(top: 100),
              child: Center(
                child: Text('No tasks for this session.',
                    style: TextStyle(color: AppColors.dim)),
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
          style: const TextStyle(
            fontFamily: 'monospace',
            fontSize: 12,
            fontWeight: FontWeight.w700,
            color: AppColors.dim,
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
        color: AppColors.card,
        border: Border.all(color: AppColors.border),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(top: 1, right: 8),
            child: Icon(_statusIcon(task.status),
                size: 16, color: _statusColor(task.status)),
          ),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(
                  '#${task.id}  $title',
                  style: _mono.copyWith(
                    color: done ? AppColors.dim : AppColors.text,
                    decoration:
                        done ? TextDecoration.lineThrough : TextDecoration.none,
                  ),
                ),
                if (task.description.isNotEmpty) ...[
                  const SizedBox(height: 4),
                  Text(
                    task.description,
                    style: _mono.copyWith(color: AppColors.dim, fontSize: 11),
                    maxLines: 4,
                    overflow: TextOverflow.ellipsis,
                  ),
                ],
                if (task.blockedBy.isNotEmpty) ...[
                  const SizedBox(height: 4),
                  Text(
                    'blocked by ${task.blockedBy.map((b) => '#$b').join(', ')}',
                    style: _mono.copyWith(
                        color: const Color(0xFFfb4934), fontSize: 11),
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

  static Color _statusColor(TaskStatus s) {
    switch (s) {
      case TaskStatus.completed:
        return const Color(0xFF8ec07c); // green
      case TaskStatus.inProgress:
        return const Color(0xFFfe8019); // orange (agent color)
      case TaskStatus.pending:
      case TaskStatus.unknown:
        return AppColors.dim;
    }
  }
}
