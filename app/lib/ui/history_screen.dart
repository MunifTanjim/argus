import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/history.dart';
import '../state/gateway.dart';
import '../state/history_view_model.dart';
import '../transport/connection.dart';
import 'filter_title.dart';
import 'history_details.dart';
import 'history_format.dart';
import 'history_group_header.dart';
import 'history_sessions_screen.dart';
import 'responsive.dart';
import 'shell_drawer.dart';
import 'theme.dart';

class HistoryScreen extends ConsumerStatefulWidget {
  const HistoryScreen({super.key});

  @override
  ConsumerState<HistoryScreen> createState() => _HistoryScreenState();
}

class _HistoryScreenState extends ConsumerState<HistoryScreen> {
  /// The project filter: null while the filter field is closed.
  String? _query;

  @override
  Widget build(BuildContext context) {
    // Re-fetch when the connection comes back.
    ref.listen<ConnState>(connStateProvider, (prev, next) {
      if (next == ConnState.connected && prev != ConnState.connected) {
        ref.read(historyProjectsProvider.notifier).reload();
      }
    });

    final projects = ref.watch(historyProjectsProvider);

    return Scaffold(
      appBar: AppBar(
        leading: shellMenuButton(context),
        title: FilterTitle(
          query: _query,
          hint: 'Filter projects',
          onChanged: (q) => setState(() => _query = q),
          onClose: () => setState(() => _query = null),
          child: const Text('History'),
        ),
        actions: [
          FilterButton(
            query: _query,
            tooltip: 'Filter projects',
            onOpen: () => setState(() => _query = ''),
          ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: () => ref.read(historyProjectsProvider.notifier).reload(),
        child: switch (projects) {
          AsyncError(:final error) => ListView(
            children: [Center(child: Text(error.toString()))],
          ),
          AsyncData(:final value) =>
            _ProjectList(projects: value, query: _query ?? ''),
          _ => const Center(child: CircularProgressIndicator()),
        },
      ),
    );
  }
}

class _ProjectList extends StatefulWidget {
  const _ProjectList({required this.projects, required this.query});

  final List<HistoryProject> projects;
  final String query;

  @override
  State<_ProjectList> createState() => _ProjectListState();
}

class _ProjectListState extends State<_ProjectList> {
  /// Group keys (node ids, '' for no node) whose projects are hidden.
  final _folded = <String>{};

  void _toggle(String key) => setState(() {
        if (!_folded.remove(key)) _folded.add(key);
      });

  @override
  Widget build(BuildContext context) {
    if (widget.projects.isEmpty) {
      return ListView(
        children: const [Center(child: Text('No past sessions found.'))],
      );
    }

    final query = widget.query;
    final shown =
        widget.projects.where((p) => matchesHistoryProject(p, query)).toList();
    if (shown.isEmpty) {
      return ListView(
        children: const [Center(child: Text('No projects match.'))],
      );
    }

    // A filter shows every match, folded or not.
    final folded = query.isEmpty ? _folded : const <String>{};
    final rows = _groupByNode(shown, folded);
    return CenteredBody(
      child: ListView.builder(
        itemCount: rows.length,
        itemBuilder: (context, index) {
          final row = rows[index];
          return switch (row) {
            _NodeHeader() => HistoryGroupHeader(
              key: Key('history-node-${row.key}'),
              icon: Icons.dns_outlined,
              label: row.label,
              count: row.count,
              folded: row.folded,
              onTap: () => _toggle(row.key),
            ),
            _ProjectEntry(:final project) => _ProjectRow(
              project: project,
              onTap: () => Navigator.push(
                context,
                MaterialPageRoute(
                  builder: (_) => HistorySessionsScreen(project: project),
                ),
              ),
            ),
          };
        },
      ),
    );
  }
}

/// A flattened row in the grouped project list: either a node header or a
/// project under the current node.
sealed class _Row {}

class _NodeHeader extends _Row {
  _NodeHeader(this.key, this.label, this.count, {required this.folded});
  final String key;
  final String label;
  final int count;
  final bool folded;
}

class _ProjectEntry extends _Row {
  _ProjectEntry(this.project);
  final HistoryProject project;
}

/// Groups a recency-sorted project list by origin node: groups appear in
/// first-occurrence (newest-activity) order, recency order preserved within
/// each, every group led by a header row. A group whose key is in [folded]
/// keeps only its header.
List<_Row> _groupByNode(List<HistoryProject> projects, Set<String> folded) {
  final order = <String>[];
  final buckets = <String, List<HistoryProject>>{};
  for (final p in projects) {
    final key = p.nodeId ?? '';
    if (!buckets.containsKey(key)) order.add(key);
    (buckets[key] ??= []).add(p);
  }
  final rows = <_Row>[];
  for (final key in order) {
    final group = buckets[key]!;
    final isFolded = folded.contains(key);
    rows.add(_NodeHeader(key, _nodeLabel(group.first), group.length,
        folded: isFolded));
    if (!isFolded) rows.addAll(group.map(_ProjectEntry.new));
  }
  return rows;
}

/// The human name for a project's origin node, falling back to the node id and
/// then a local placeholder (direct node connections carry no node info).
String _nodeLabel(HistoryProject p) =>
    firstLabel([p.nodeLabel, p.nodeId]) ?? 'This machine';

class _ProjectRow extends StatelessWidget {
  const _ProjectRow({required this.project, required this.onTap});

  final HistoryProject project;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final n = project.sessionCount;
    final ago = shortAgo(project.lastActivity);
    final count = n == 1 ? '1 session' : '$n sessions';
    // The node is named by the group header above. The path stays on the row:
    // worktrees and subfolders of one repo share its label.
    return ListTile(
      title: Row(
        children: [
          Expanded(
            child: Row(
              children: [
                Flexible(
                  child: Text(
                    project.label,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                if (project.missing) ...[
                  const SizedBox(width: 6),
                  _MissingLabel(
                      key: Key('history-missing-${project.projectDir}')),
                ],
              ],
            ),
          ),
          if (ago.isNotEmpty) ...[
            const SizedBox(width: 8),
            Text(ago, style: TextStyle(fontSize: 11, color: t.textDim)),
          ],
        ],
      ),
      subtitle: Text(
        project.cwd.isEmpty ? count : '$count · ${shortenPath(project.cwd)}',
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
      ),
      onTap: onTap,
      onLongPress: project.cwd.isEmpty
          ? null
          : () => showHistoryProjectDetails(context, project),
    );
  }
}

class _MissingLabel extends StatelessWidget {
  const _MissingLabel({super.key});

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 4),
      decoration: BoxDecoration(
        border: Border.all(color: t.line),
        borderRadius: BorderRadius.circular(4),
      ),
      child: Text(
        'missing',
        style: TextStyle(fontSize: 10.5, color: t.textDim),
      ),
    );
  }
}
