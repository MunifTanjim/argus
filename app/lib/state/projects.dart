import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/project.dart';
import '../transport/gateway_client.dart';

class ProjectsState {
  const ProjectsState({
    this.projects = const [],
    this.loaded = false,
    this.error,
  });

  final List<ProjectNode> projects;
  final bool loaded;
  final String? error; // the last load failed; projects are the last good list
}

/// The merged project tree of every connected node. A failed load keeps the
/// last good list, so a reconnect blip does not empty the drawer; a node that
/// failed while others answered keeps its last good projects.
class ProjectsNotifier extends Notifier<ProjectsState> {
  var _generation = 0;

  @override
  ProjectsState build() => const ProjectsState();

  Future<void> load(GatewayClient? client) async {
    if (client == null) return;
    final gen = ++_generation;
    try {
      final res = await client.call('project.list');
      if (gen != _generation) return;
      final failed = res is Map ? res['failed_nodes'] : null;
      final kept = failed is List && failed.isNotEmpty
          ? [
              for (final p in state.projects)
                if (failed.contains(p.nodeId)) p,
            ]
          : const <ProjectNode>[];
      state = ProjectsState(
        projects: [...parseProjectList(res), ...kept],
        loaded: true,
      );
    } catch (e) {
      if (gen != _generation) return;
      state = ProjectsState(
        projects: state.projects,
        loaded: state.loaded,
        error: '$e',
      );
    }
  }

  void clear() {
    _generation++;
    state = const ProjectsState();
  }
}

final projectsProvider = NotifierProvider<ProjectsNotifier, ProjectsState>(
  ProjectsNotifier.new,
);
