enum TaskStatus { pending, inProgress, completed, unknown }

const _statusByName = {
  'pending': TaskStatus.pending,
  'in_progress': TaskStatus.inProgress,
  'completed': TaskStatus.completed,
};

TaskStatus taskStatusFrom(String s) => _statusByName[s] ?? TaskStatus.unknown;

/// One entry in a session's Claude Code task list (TaskCreate/TaskUpdate),
/// mirroring api.Task on the wire.
class Task {
  const Task({
    required this.id,
    required this.subject,
    this.description = '',
    this.activeForm = '',
    required this.status,
    this.blocks = const [],
    this.blockedBy = const [],
  });

  final String id;
  final String subject;
  final String description;
  final String activeForm; // spinner label while in_progress
  final TaskStatus status;
  final List<String> blocks;
  final List<String> blockedBy;

  factory Task.fromJson(Map<String, dynamic> j) => Task(
        id: j['id'] as String? ?? '',
        subject: j['subject'] as String? ?? '',
        description: j['description'] as String? ?? '',
        activeForm: j['active_form'] as String? ?? '',
        status: taskStatusFrom(j['status'] as String? ?? ''),
        blocks: ((j['blocks'] as List?) ?? const [])
            .map((e) => e.toString())
            .toList(),
        blockedBy: ((j['blocked_by'] as List?) ?? const [])
            .map((e) => e.toString())
            .toList(),
      );
}
