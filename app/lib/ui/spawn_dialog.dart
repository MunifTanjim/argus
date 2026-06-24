import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../state/gateway.dart';
import '../state/grouping.dart';
import '../state/sessions.dart';
import '../state/spawn_view_model.dart';

Future<void> showSpawnDialog(BuildContext context, WidgetRef ref) {
  return showDialog(
    context: context,
    builder: (_) => const SpawnDialog(),
  );
}

class SpawnDialog extends ConsumerStatefulWidget {
  const SpawnDialog({super.key});

  @override
  ConsumerState<SpawnDialog> createState() => _SpawnDialogState();
}

class _SpawnDialogState extends ConsumerState<SpawnDialog> {
  late final SpawnViewModel _vm;
  // Nodes from nodes.list; preferred over session-derived nodes so a target is
  // offered even with zero sessions. Empty until the call returns.
  List<NodeRef> _remoteNodes = const [];
  String? _nodeId;
  String _name = '';
  String _cwd = '';
  String _cmd = '';

  @override
  void initState() {
    super.initState();
    _vm = SpawnViewModel(ref.read(sessionRepositoryProvider));
    _vm.spawn.addListener(_onCommand);
    final nodes = nodesFromSessions(ref.read(sessionsProvider).values);
    if (nodes.length >= 2) {
      _nodeId = nodes.first.id;
    }
    _loadNodes();
  }

  Future<void> _loadNodes() async {
    final result = await ref.read(sessionRepositoryProvider).nodes();
    if (!mounted) return;
    switch (result) {
      case Ok(:final value) when value.isNotEmpty:
        setState(() {
          _remoteNodes = value;
          if (value.length >= 2 &&
              (_nodeId == null || !value.any((n) => n.id == _nodeId))) {
            _nodeId = value.first.id;
          }
        });
      case Ok():
      case Error():
        break; // fall back to session-derived nodes
    }
  }

  void _onCommand() {
    if (mounted) setState(() {}); // reflect running state on the buttons
  }

  @override
  void dispose() {
    _vm.spawn.removeListener(_onCommand);
    _vm.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final sessionNodes = nodesFromSessions(ref.watch(sessionsProvider).values);
    final nodes = _remoteNodes.isNotEmpty ? _remoteNodes : sessionNodes;
    final busy = _vm.spawn.running;

    return AlertDialog(
      title: const Text('New session'),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (nodes.length >= 2)
              DropdownButton<String>(
                value: _nodeId,
                isExpanded: true,
                items: [
                  for (final n in nodes)
                    DropdownMenuItem(value: n.id, child: Text(n.label)),
                ],
                onChanged: (v) => setState(() => _nodeId = v),
              ),
            TextField(
              decoration: const InputDecoration(labelText: 'Name'),
              onChanged: (v) => setState(() => _name = v),
            ),
            TextField(
              decoration: const InputDecoration(labelText: 'Working directory'),
              onChanged: (v) => setState(() => _cwd = v),
            ),
            TextField(
              decoration:
                  const InputDecoration(labelText: 'Command', hintText: 'claude'),
              onChanged: (v) => setState(() => _cmd = v),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: busy ? null : () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        TextButton(
          onPressed: _name.trim().isEmpty || busy ? null : _spawn,
          child: busy
              ? const SizedBox(
                  width: 16,
                  height: 16,
                  child: CircularProgressIndicator(strokeWidth: 2),
                )
              : const Text('Spawn'),
        ),
      ],
    );
  }

  String? _trimOrNull(String s) {
    final t = s.trim();
    return t.isEmpty ? null : t;
  }

  Future<void> _spawn() async {
    await _vm.spawn.execute(SpawnRequest(
      nodeId: _nodeId,
      name: _name.trim(),
      cwd: _trimOrNull(_cwd),
      command: _trimOrNull(_cmd),
    ));
    if (!mounted) return;
    switch (_vm.spawn.result) {
      case Ok():
        Navigator.of(context).pop();
        final client = ref.read(gatewayProvider)?.client;
        if (client != null) {
          await refreshSessions(client, ref.read(sessionsProvider.notifier));
        }
      case Error(:final error):
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed: $error')),
        );
      case null:
        break;
    }
  }
}
