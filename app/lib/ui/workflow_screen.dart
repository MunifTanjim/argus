import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/workflow_repository.dart';
import '../models/entry.dart';
import '../models/session.dart';
import '../models/workflow.dart';
import '../state/gateway.dart';
import '../state/sessions.dart';
import '../state/tool_detail.dart';
import '../transport/connection.dart';
import 'subagent_trace_screen.dart';
import 'theme.dart';

const _mono = TextStyle(fontFamily: 'monospace', fontSize: 12, height: 1.3);
final _monoDim = _mono.copyWith(color: AppColors.dim);

/// How often a live page opened from a subagent trace refetches while its run
/// is running; the session's running workflows list main-session runs only.
const workflowPollEvery = Duration(seconds: 2);

/// A Workflow run's phases and agents.
class WorkflowScreen extends ConsumerStatefulWidget {
  const WorkflowScreen({
    super.key,
    required this.parentRef,
    required this.runId,
  });

  /// The transcript that launched the run; its [ToolDetailRef.agentId] is the
  /// subagent trace it was launched from (null = the main transcript).
  final ToolDetailRef parentRef;
  final String runId;

  @override
  ConsumerState<WorkflowScreen> createState() => _WorkflowScreenState();
}

class _WorkflowScreenState extends ConsumerState<WorkflowScreen> {
  WorkflowView? _view;
  Object? _error;
  Timer? _poll;
  int _fetches = 0;

  ToolDetailRef get _ref => widget.parentRef;
  String? get _sessionId => _ref.sessionId;
  bool get _polls => !_ref.isHistory && (_ref.agentId ?? '').isNotEmpty;

  @override
  void initState() {
    super.initState();
    _fetch();
  }

  @override
  void dispose() {
    _poll?.cancel();
    super.dispose();
  }

  Future<void> _fetch() async {
    final n = ++_fetches;
    _poll?.cancel();
    final repo = ref.read(workflowRepositoryProvider);
    final res = await repo.fetch(_ref, widget.runId);
    // A later fetch, or a closed page, supersedes this one.
    if (!mounted || n != _fetches) return;
    // The page keeps the last view; report the failed refetch over it.
    if (res case Error(error: final e) when _view != null) {
      ScaffoldMessenger.of(
        context,
      ).showSnackBar(SnackBar(content: Text('workflow: $e')));
    }
    setState(() {
      switch (res) {
        case Ok(value: final v):
          _view = v;
          _error = null;
        case Error(error: final e):
          _error = e;
      }
    });
    if (res is Ok && _polls && _view!.ref.status == 'running') {
      _poll = Timer(workflowPollEvery, _fetch);
    }
  }

  /// The run's progress on the session, null once it is no longer running.
  (int, int)? _progress(Session? s) {
    for (final w in s?.workflows ?? const <RunningWorkflow>[]) {
      if (w.runId == widget.runId) return (w.done, w.total);
    }
    return null;
  }

  @override
  Widget build(BuildContext context) {
    if (!_ref.isHistory) {
      ref.listen<ConnState>(connStateProvider, (prev, next) {
        if (next == ConnState.connected && prev != ConnState.connected) {
          _fetch();
        }
      });
      ref.listen<bool?>(
        sessionsProvider.select((m) => m[_sessionId]?.offline),
        (prev, next) {
          if (next == false && prev != false) _fetch();
        },
      );
      ref.listen<(int, int)?>(
        sessionsProvider.select((m) => _progress(m[_sessionId])),
        (_, _) => _fetch(),
      );
    }

    final v = _view;
    final name = (v?.ref.name ?? '').isNotEmpty ? v!.ref.name : 'Workflow';
    final status = v?.ref.status ?? '';
    return Scaffold(
      appBar: AppBar(title: Text(status.isEmpty ? name : '$name · $status')),
      body: SafeArea(
        top: false, // AppBar insets top; bottom clears the system nav bar.
        child: v != null
            ? _phases(context, v)
            : _error != null
            ? Center(child: Text('Failed to load workflow: $_error'))
            : const Center(child: CircularProgressIndicator()),
      ),
    );
  }

  Widget _phases(BuildContext context, WorkflowView v) {
    final rows = <Widget>[];
    for (var i = 0; i < v.phases.length; i++) {
      final p = v.phases[i];
      if (p.title.isNotEmpty || v.phases.length > 1) {
        rows.add(_PhaseHeader(p.title.isNotEmpty ? p.title : 'Phase ${i + 1}'));
      }
      for (final a in p.agents) {
        rows.add(
          _AgentRow(
            agent: a,
            onTap: a.hasTrace ? () => _openTrace(context, a) : null,
          ),
        );
      }
    }
    return ListView(
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      children: rows,
    );
  }

  void _openTrace(BuildContext context, WorkflowAgent a) {
    Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => SubagentTraceScreen(
          parentRef: _ref,
          item: Entry(
            id: a.id,
            kind: EntryKind.subagent,
            subagents: [
              Subagent(
                id: a.id,
                name: a.label,
                status: a.state,
                hasTrace: true,
              ),
            ],
          ),
        ),
      ),
    );
  }
}

class _PhaseHeader extends StatelessWidget {
  const _PhaseHeader(this.title);
  final String title;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(top: 10, bottom: 4),
    child: Row(
      children: [
        const SizedBox(
          width: 16,
          child: Divider(color: AppColors.border, height: 1),
        ),
        Padding(
          padding: const EdgeInsets.symmetric(horizontal: 8),
          child: Text(title, style: _monoDim),
        ),
        const Expanded(child: Divider(color: AppColors.border, height: 1)),
      ],
    ),
  );
}

class _AgentRow extends StatelessWidget {
  const _AgentRow({required this.agent, this.onTap});
  final WorkflowAgent agent;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) {
    final label = agent.label.isNotEmpty ? agent.label : agent.id;
    final row = Padding(
      padding: const EdgeInsets.symmetric(vertical: 6),
      child: Row(
        children: [
          const Icon(
            Icons.smart_toy_outlined,
            size: 14,
            color: AppColors.accent,
          ),
          const SizedBox(width: 6),
          Flexible(
            child: Text(
              label,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
              style: _mono.copyWith(
                color: AppColors.accent,
                fontWeight: FontWeight.w600,
              ),
            ),
          ),
          if (agent.state.isNotEmpty) ...[
            const SizedBox(width: 8),
            Text(
              agent.state,
              style: _mono.copyWith(color: AppColors.secondary),
            ),
          ],
          const Spacer(),
          if (onTap != null) Text(' ›', style: _monoDim),
        ],
      ),
    );
    return onTap == null ? row : InkWell(onTap: onTap, child: row);
  }
}
