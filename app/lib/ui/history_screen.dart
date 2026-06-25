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

    return CenteredBody(
      child: ListView.builder(
        itemCount: projects.length,
        itemBuilder: (context, index) {
          final p = projects[index];
          return _ProjectCard(
            project: p,
            onTap: () => Navigator.push(
              context,
              MaterialPageRoute(
                builder: (_) => HistorySessionsScreen(project: p),
              ),
            ),
          );
        },
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

    return ListTile(
      title: Row(
        children: [
          Expanded(child: Text(project.label)),
          if (project.nodeLabel != null && project.nodeLabel!.isNotEmpty)
            Container(
              padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
              decoration: BoxDecoration(
                color: Theme.of(context).colorScheme.secondaryContainer,
                borderRadius: BorderRadius.circular(4),
              ),
              child: Text(
                project.nodeLabel!,
                style: Theme.of(context).textTheme.labelSmall,
              ),
            ),
        ],
      ),
      subtitle: Text(subtitleParts.join(' · ')),
      onTap: onTap,
    );
  }
}
