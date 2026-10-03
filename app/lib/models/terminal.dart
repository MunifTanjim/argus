/// A persistent shell on a node (one window of its terminal tmux session).
/// `id` is composite (`<node>:<window>`).
class NodeTerminal {
  const NodeTerminal({
    required this.id,
    this.nodeId = '',
    this.nodeLabel = '',
    this.name = '',
    this.cwd = '',
    this.command = '',
    this.attached = false,
  });

  factory NodeTerminal.fromJson(Map<String, dynamic> j) => NodeTerminal(
        id: j['id'] as String? ?? '',
        nodeId: j['node_id'] as String? ?? '',
        nodeLabel: j['node_label'] as String? ?? '',
        name: j['name'] as String? ?? '',
        cwd: j['cwd'] as String? ?? '',
        command: j['command'] as String? ?? '',
        attached: j['attached'] as bool? ?? false,
      );

  final String id;
  final String nodeId;
  final String nodeLabel;
  final String name;
  final String cwd;
  final String command;
  final bool attached;

  String get title =>
      name.isNotEmpty ? name : (command.isNotEmpty ? command : 'terminal');
}

List<NodeTerminal> parseTerminalList(Object? result) {
  final list = result is Map ? result['terminals'] : null;
  if (list is! List) return const [];
  return [
    for (final t in list)
      if (t is Map<String, dynamic>) NodeTerminal.fromJson(t),
  ];
}
