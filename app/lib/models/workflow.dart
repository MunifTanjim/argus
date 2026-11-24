import 'entry.dart';

class WorkflowAgent {
  final String id;
  final String label;
  final String state;
  final bool hasTrace;

  const WorkflowAgent({
    this.id = '',
    this.label = '',
    this.state = '',
    this.hasTrace = false,
  });

  factory WorkflowAgent.fromJson(Map<String, dynamic> j) => WorkflowAgent(
    id: j['id'] as String? ?? '',
    label: j['label'] as String? ?? '',
    state: j['state'] as String? ?? '',
    hasTrace: j['hasTrace'] as bool? ?? false,
  );
}

class WorkflowPhase {
  final String title;
  final List<WorkflowAgent> agents;

  const WorkflowPhase({this.title = '', this.agents = const []});

  factory WorkflowPhase.fromJson(Map<String, dynamic> j) => WorkflowPhase(
    title: j['title'] as String? ?? '',
    agents:
        (j['agents'] as List?)
            ?.map((e) => WorkflowAgent.fromJson(e as Map<String, dynamic>))
            .toList() ??
        const [],
  );
}

/// A workflow run's phases and agents, from sessions.workflow /
/// sessions.historyWorkflow.
class WorkflowView {
  final WorkflowRef ref;
  final List<WorkflowPhase> phases;

  const WorkflowView({required this.ref, this.phases = const []});

  factory WorkflowView.fromJson(Map<String, dynamic> j) => WorkflowView(
    ref: WorkflowRef.fromJson(j),
    phases:
        (j['phases'] as List?)
            ?.map((e) => WorkflowPhase.fromJson(e as Map<String, dynamic>))
            .toList() ??
        const [],
  );
}
