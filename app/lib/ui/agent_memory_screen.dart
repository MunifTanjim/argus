import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/pagent.dart';
import '../state/pagents.dart';
import '../state/projects_api.dart' show actionError;
import 'pagent_style.dart';
import 'theme.dart';

/// Opens an agent's memory: its files, change history and staged proposals.
Route<void> agentMemoryRoute(String agentId) =>
    MaterialPageRoute(builder: (_) => AgentMemoryScreen(agentId: agentId));

class AgentMemoryScreen extends ConsumerWidget {
  const AgentMemoryScreen({super.key, required this.agentId});

  final String agentId;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final name = ref.watch(
      pagentsProvider.select(
        (s) => s.agents.where((a) => a.id == agentId).firstOrNull?.name ?? '',
      ),
    );
    return DefaultTabController(
      length: 3,
      child: Scaffold(
        appBar: AppBar(
          title: Text(name.isEmpty ? 'Memory' : '$name · Memory'),
          bottom: const TabBar(
            tabs: [
              Tab(text: 'Files'),
              Tab(text: 'History'),
              Tab(text: 'Review'),
            ],
          ),
        ),
        body: TabBarView(
          children: [
            _FilesTab(agentId: agentId),
            _HistoryTab(agentId: agentId),
            _ReviewTab(agentId: agentId),
          ],
        ),
      ),
    );
  }
}

void _say(BuildContext context, String text) => ScaffoldMessenger.of(context)
  ..hideCurrentSnackBar()
  ..showSnackBar(SnackBar(content: Text(text)));

Widget _centered(String text) => Center(
  child: Padding(
    padding: const EdgeInsets.all(24),
    child: Text(text, style: const TextStyle(color: AppColors.dim)),
  ),
);

/// Loads a value on mount and again when the agents change (any node's
/// `pagent.changed`) or [reload] is called.
abstract class _LoadingState<W extends ConsumerStatefulWidget, T>
    extends ConsumerState<W> {
  T? value;
  Object? error;

  Future<T> fetch(PAgentsApi api);

  @override
  void initState() {
    super.initState();
    reload();
  }

  Future<void> reload() async {
    try {
      final v = await fetch(ref.read(pagentsApiProvider));
      if (mounted) {
        setState(() {
          value = v;
          error = null;
        });
      }
    } catch (e) {
      if (mounted) setState(() => error = e);
    }
  }

  /// A body for the loading and error states, or null once loaded.
  Widget? pending() {
    ref.listen(pagentsProvider.select((s) => s.agents), (_, _) => reload());
    if (value != null) return null;
    return error != null
        ? _centered(actionError(error!))
        : const Center(child: CircularProgressIndicator());
  }
}

class _FilesTab extends ConsumerStatefulWidget {
  const _FilesTab({required this.agentId});
  final String agentId;

  @override
  ConsumerState<_FilesTab> createState() => _FilesTabState();
}

class _FilesTabState extends _LoadingState<_FilesTab, PAgentMemory> {
  @override
  Future<PAgentMemory> fetch(PAgentsApi api) => api.memory(widget.agentId);

  @override
  Widget build(BuildContext context) {
    final wait = pending();
    if (wait != null) return wait;
    final m = value!;
    return ListView(
      padding: const EdgeInsets.all(12),
      children: [
        _file(
          'MEMORY.md',
          m.memory,
          trailing:
              '${kbSize(m.usage)}${m.budget > 0 ? ' / ${kbSize(m.budget)}' : ''}',
        ),
        _file('USER.md', m.user),
        _file('PROFILE.md', m.profile),
        if (m.topics.isNotEmpty) ...[
          const Padding(
            padding: EdgeInsets.fromLTRB(4, 12, 4, 4),
            child: Text('Topics', style: TextStyle(color: AppColors.dim)),
          ),
          for (final t in m.topics)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
              child: Text(t, style: const TextStyle(fontFamily: 'monospace')),
            ),
        ],
      ],
    );
  }

  Widget _file(String name, String body, {String? trailing}) => Card(
    color: AppColors.card,
    child: ExpansionTile(
      key: ValueKey(name),
      title: Text(name),
      subtitle: body.trim().isEmpty ? const Text('empty') : null,
      trailing: trailing == null
          ? null
          : Text(trailing, style: const TextStyle(color: AppColors.dim)),
      childrenPadding: const EdgeInsets.fromLTRB(16, 0, 16, 12),
      expandedAlignment: Alignment.centerLeft,
      children: [
        if (body.trim().isNotEmpty)
          SelectableText(
            body,
            style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
          ),
      ],
    ),
  );
}

class _HistoryTab extends ConsumerStatefulWidget {
  const _HistoryTab({required this.agentId});
  final String agentId;

  @override
  ConsumerState<_HistoryTab> createState() => _HistoryTabState();
}

class _HistoryTabState
    extends _LoadingState<_HistoryTab, List<PAgentMemoryCommit>> {
  bool _profile = false;

  @override
  Future<List<PAgentMemoryCommit>> fetch(PAgentsApi api) =>
      api.memoryLog(widget.agentId, profile: _profile);

  Future<void> _undo(PAgentMemoryCommit c) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Undo this change?'),
        content: Text(c.subject),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Cancel'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Undo'),
          ),
        ],
      ),
    );
    if (ok != true || !mounted) return;
    try {
      await ref
          .read(pagentsApiProvider)
          .memoryUndo(widget.agentId, c.hash, profile: _profile);
    } catch (e) {
      if (mounted) _say(context, 'undo: ${actionError(e)}');
    }
    ref.invalidate(agentMemoryUsageProvider(widget.agentId));
    await reload();
  }

  @override
  Widget build(BuildContext context) {
    final wait = pending();
    if (wait != null) return wait;
    final commits = value!;
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.fromLTRB(12, 8, 12, 0),
          child: Align(
            alignment: Alignment.centerLeft,
            child: FilterChip(
              label: const Text('Shared profile'),
              selected: _profile,
              onSelected: (v) {
                setState(() {
                  _profile = v;
                  value = null;
                });
                reload();
              },
            ),
          ),
        ),
        Expanded(
          child: commits.isEmpty
              ? _centered('no changes')
              : ListView(
                  children: [
                    for (final c in commits)
                      ListTile(
                        title: Text(c.subject),
                        subtitle: Text(
                          [
                            if (c.source.isNotEmpty) c.source,
                            agoText(c.when),
                          ].where((p) => p.isNotEmpty).join(' · '),
                        ),
                        trailing: IconButton(
                          tooltip: 'Undo',
                          icon: const Icon(Icons.undo),
                          onPressed: () => _undo(c),
                        ),
                      ),
                  ],
                ),
        ),
      ],
    );
  }
}

class _ReviewTab extends ConsumerStatefulWidget {
  const _ReviewTab({required this.agentId});
  final String agentId;

  @override
  ConsumerState<_ReviewTab> createState() => _ReviewTabState();
}

class _ReviewTabState
    extends _LoadingState<_ReviewTab, List<PAgentMemoryProposal>> {
  final _busy = <String>{};

  @override
  Future<List<PAgentMemoryProposal>> fetch(PAgentsApi api) =>
      api.proposals(widget.agentId);

  Future<void> _decide(PAgentMemoryProposal p, bool accept) async {
    setState(() => _busy.add(p.id));
    try {
      final r = await ref.read(pagentsApiProvider).decide(p.id, accept);
      if (mounted && accept) {
        _say(
          context,
          r.rejected.isEmpty
              ? 'applied ${r.applied}'
              : 'applied ${r.applied}, rejected: ${r.rejected.join('; ')}',
        );
      }
    } catch (e) {
      if (mounted) _say(context, '${accept ? 'accept' : 'reject'}: ${actionError(e)}');
    }
    if (mounted) setState(() => _busy.remove(p.id));
    ref.invalidate(agentMemoryUsageProvider(widget.agentId));
    await reload();
  }

  @override
  Widget build(BuildContext context) {
    final wait = pending();
    if (wait != null) return wait;
    final proposals = value!;
    if (proposals.isEmpty) return _centered('nothing to review');
    return ListView(
      padding: const EdgeInsets.all(12),
      children: [for (final p in proposals) _card(p)],
    );
  }

  Widget _card(PAgentMemoryProposal p) {
    final busy = _busy.contains(p.id);
    return Card(
      color: AppColors.card,
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              p.isRoutine
                  ? 'Routine change'
                  : p.isInstructions
                  ? 'Instruction rewrite'
                  : 'Memory change',
              style: const TextStyle(
                color: AppColors.secondary,
                fontWeight: FontWeight.w700,
              ),
            ),
            if (p.source.isNotEmpty)
              Text(
                p.source,
                style: const TextStyle(color: AppColors.dim, fontSize: 12),
              ),
            if (p.isRoutine && (p.slug.isNotEmpty || p.summary.isNotEmpty)) ...[
              const SizedBox(height: 6),
              Text([p.slug, p.summary].where((x) => x.isNotEmpty).join(' · ')),
            ],
            if (p.reason.isNotEmpty) ...[
              const SizedBox(height: 6),
              Text(p.reason),
            ],
            if (p.isInstructions && p.text.isNotEmpty) ...[
              const SizedBox(height: 8),
              SelectableText(
                p.text,
                style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
              ),
            ],
            for (final op in p.ops) ...[
              const SizedBox(height: 8),
              Text(
                '${op.action} ${op.file.isEmpty ? 'MEMORY.md' : op.file}',
                style: const TextStyle(color: AppColors.dim, fontSize: 12),
              ),
              if (op.match.isNotEmpty) Text('match: ${op.match}'),
              if (op.text.isNotEmpty) Text(op.text),
              if (op.until.isNotEmpty)
                Text(
                  'until ${op.until}',
                  style: const TextStyle(color: AppColors.dim, fontSize: 12),
                ),
            ],
            const SizedBox(height: 8),
            Row(
              mainAxisAlignment: MainAxisAlignment.end,
              children: [
                TextButton(
                  onPressed: busy ? null : () => _decide(p, false),
                  child: const Text('Reject'),
                ),
                const SizedBox(width: 8),
                FilledButton(
                  onPressed: busy ? null : () => _decide(p, true),
                  child: const Text('Accept'),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
