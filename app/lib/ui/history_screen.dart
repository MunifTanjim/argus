import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/history.dart';
import '../state/gateway.dart';
import '../state/history_view_model.dart';
import '../transport/connection.dart';
import 'history_sessions_screen.dart';
import 'relative_time.dart';
import 'responsive.dart';

class HistoryScreen extends ConsumerWidget {
  const HistoryScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // Re-fetch when the connection comes back.
    ref.listen<ConnState>(connStateProvider, (prev, next) {
      if (next == ConnState.connected && prev != ConnState.connected) {
        ref.read(historyProjectsProvider.notifier).reload();
      }
    });

    final projects = ref.watch(historyProjectsProvider);

    return Scaffold(
      appBar: AppBar(title: const Text('History')),
      body: RefreshIndicator(
        onRefresh: () => ref.read(historyProjectsProvider.notifier).reload(),
        child: switch (projects) {
          AsyncError(:final error) => ListView(
            children: [Center(child: Text(error.toString()))],
          ),
          AsyncData(:final value) => _ProjectList(projects: value),
          _ => const Center(child: CircularProgressIndicator()),
        },
      ),
    );
  }
}

class _ProjectList extends StatelessWidget {
  const _ProjectList({required this.projects});

  final List<HistoryProject> projects;

  @override
  Widget build(BuildContext context) {
    if (projects.isEmpty) {
      return ListView(
        children: const [Center(child: Text('No past sessions found.'))],
      );
    }

    final rows = _groupByNode(projects);
    return CenteredBody(
      child: ListView.builder(
        itemCount: rows.length,
        itemBuilder: (context, index) {
          final row = rows[index];
          return switch (row) {
            _NodeHeader(:final label) => _NodeHeaderTile(label: label),
            _ProjectEntry(:final project) => _ProjectCard(
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
  _NodeHeader(this.label);
  final String label;
}

class _ProjectEntry extends _Row {
  _ProjectEntry(this.project);
  final HistoryProject project;
}

/// Groups a recency-sorted project list by origin node: groups appear in
/// first-occurrence (newest-activity) order, recency order preserved within
/// each, every group led by a header row.
List<_Row> _groupByNode(List<HistoryProject> projects) {
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
    rows.add(_NodeHeader(_nodeLabel(group.first)));
    rows.addAll(group.map(_ProjectEntry.new));
  }
  return rows;
}

/// The human name for a project's origin node, falling back to the node id and
/// then a local placeholder (direct node connections carry no node info).
String _nodeLabel(HistoryProject p) {
  final label = p.nodeLabel;
  if (label != null && label.isNotEmpty) return label;
  final id = p.nodeId;
  if (id != null && id.isNotEmpty) return id;
  return 'This machine';
}

class _NodeHeaderTile extends StatelessWidget {
  const _NodeHeaderTile({required this.label});

  final String label;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
      child: Row(
        children: [
          Icon(Icons.dns_outlined,
              size: 16, color: Theme.of(context).colorScheme.secondary),
          const SizedBox(width: 6),
          Text(
            label,
            style: Theme.of(context).textTheme.titleSmall?.copyWith(
                  color: Theme.of(context).colorScheme.secondary,
                  fontWeight: FontWeight.w600,
                ),
          ),
        ],
      ),
    );
  }
}

class _ProjectCard extends StatelessWidget {
  const _ProjectCard({required this.project, required this.onTap});

  final HistoryProject project;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final subtitleParts = <String>[
      '${project.sessionCount} sessions',
      if (project.cwd.isNotEmpty) project.cwd,
      if (project.lastActivity.isNotEmpty) relativeTime(project.lastActivity),
    ];

    // The node is named by the group header above; the card shows only its own
    // label, counts and path.
    return ListTile(
      title: Text(project.label),
      subtitle: Text(subtitleParts.join(' · ')),
      onTap: onTap,
    );
  }
}
