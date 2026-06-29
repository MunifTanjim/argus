import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../models/history.dart';
import '../state/gateway.dart';
import '../state/grouping.dart';
import '../state/history_view_model.dart';
import '../state/sessions.dart';
import '../state/spawn_view_model.dart';

Future<void> showSpawnDialog(BuildContext context, WidgetRef ref) {
  return showDialog(
    context: context,
    builder: (_) => const SpawnDialog(),
  );
}

class SpawnDialog extends StatelessWidget {
  const SpawnDialog({super.key});

  @override
  Widget build(BuildContext context) => const AlertDialog(
        title: Text('New session'),
        content: SpawnDialogBody(),
      );
}

class SpawnDialogBody extends ConsumerStatefulWidget {
  const SpawnDialogBody({super.key});

  @override
  ConsumerState<SpawnDialogBody> createState() => _SpawnDialogBodyState();
}

class _SpawnDialogBodyState extends ConsumerState<SpawnDialogBody> {
  late final SpawnViewModel _vm;
  // Nodes from nodes.list; preferred over session-derived nodes so a target is
  // offered even with zero sessions. Empty until the call returns.
  List<NodeRef> _remoteNodes = const [];
  String? _nodeId;
  String _prompt = '';

  // Directory state
  String? _selectedCwd; // resolved when projects load or the user picks
  bool _customCwd = false;
  String _customPath = '';

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

  String? _effectiveCwd() {
    final cwd = _customCwd ? _customPath.trim() : (_selectedCwd ?? '').trim();
    return cwd.isEmpty ? null : cwd;
  }

  Future<void> _spawn() async {
    await _vm.spawn.execute(SpawnRequest(
      nodeId: _nodeId,
      cwd: _effectiveCwd(),
      prompt: _prompt.trim(),
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

  @override
  Widget build(BuildContext context) {
    final sessionNodes = nodesFromSessions(ref.watch(sessionsProvider).values);
    final nodes = _remoteNodes.isNotEmpty ? _remoteNodes : sessionNodes;
    final busy = _vm.spawn.running;

    final allProjects = ref.watch(historyProjectsProvider).value ??
        const <HistoryProject>[];
    final projects = [
      for (final p in allProjects)
        if (_nodeId == null || (p.nodeId ?? '') == _nodeId) p,
    ];
    _selectedCwd ??= projects.isNotEmpty ? projects.first.cwd : null;

    return SingleChildScrollView(
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
              onChanged: (v) => setState(() {
                _nodeId = v;
                // Reset directory selection when node changes
                _selectedCwd = null;
                _customCwd = false;
                _customPath = '';
              }),
            ),
          DropdownButton<String>(
            isExpanded: true,
            value: _customCwd ? '__custom__' : _selectedCwd,
            hint: const Text('Working directory'),
            items: [
              for (final p in projects)
                DropdownMenuItem(value: p.cwd, child: Text(p.label)),
              const DropdownMenuItem(
                  value: '__custom__', child: Text('Custom path…')),
            ],
            onChanged: (v) => setState(() {
              if (v == '__custom__') {
                _customCwd = true;
              } else {
                _customCwd = false;
                _selectedCwd = v;
              }
            }),
          ),
          if (_customCwd)
            TextField(
              decoration: const InputDecoration(labelText: 'Custom path'),
              onChanged: (v) => setState(() => _customPath = v),
            ),
          TextField(
            key: const Key('spawn-prompt'),
            decoration: const InputDecoration(
              labelText: 'Initial prompt',
              hintText: 'What should this session work on?',
            ),
            minLines: 3,
            maxLines: null,
            keyboardType: TextInputType.multiline,
            onChanged: (v) => setState(() => _prompt = v),
          ),
          const SizedBox(height: 8),
          Row(
            mainAxisAlignment: MainAxisAlignment.end,
            children: [
              TextButton(
                onPressed: busy ? null : () => Navigator.of(context).pop(),
                child: const Text('Cancel'),
              ),
              TextButton(
                onPressed: (busy || _prompt.trim().isEmpty) ? null : _spawn,
                child: busy
                    ? const SizedBox(
                        width: 16,
                        height: 16,
                        child: CircularProgressIndicator(strokeWidth: 2),
                      )
                    : const Text('Spawn'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}
