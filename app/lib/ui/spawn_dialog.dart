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
import 'theme.dart';

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
  // Nodes from server.info; preferred over session-derived nodes so a target is
  // offered even with zero sessions. Empty until the call returns.
  List<NodeRef> _remoteNodes = const [];
  String? _nodeId;
  String _prompt = '';

  // null while probing; picker shows only when >=2.
  List<AgentInfo>? _agents;
  String? _agent;

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
      _nodeId = _defaultNodeId(nodes);
    }
    _loadNodes();
    _loadAgents();
  }

  // Last call wins if the node changes mid-probe.
  Future<void> _loadAgents() async {
    final nodeId = _nodeId;
    final result = await ref.read(sessionRepositoryProvider).listAgents(nodeId);
    if (!mounted || nodeId != _nodeId) return;
    switch (result) {
      case Ok(:final value):
        final spawnable = [for (final a in value) if (a.spawnable) a];
        setState(() {
          _agents = spawnable;
          _agent = spawnable.isNotEmpty ? spawnable.first.id : null;
        });
      case Error():
        setState(() {
          _agents = const [];
          _agent = null;
        });
    }
  }

  // Prefer a spawn-capable (tmux) node as the default selection; fall back to the
  // first node so the picker still shows a (disabled) selection when none qualify.
  String? _defaultNodeId(List<NodeRef> nodes) {
    for (final n in nodes) {
      if (n.spawnSupported) return n.id;
    }
    return nodes.isNotEmpty ? nodes.first.id : null;
  }

  Future<void> _loadNodes() async {
    final result = await ref.read(sessionRepositoryProvider).nodes();
    if (!mounted) return;
    switch (result) {
      case Ok(:final value) when value.isNotEmpty:
        final prevNodeId = _nodeId;
        setState(() {
          _remoteNodes = value;
          if (value.length >= 2 &&
              (_nodeId == null || !value.any((n) => n.id == _nodeId))) {
            _nodeId = _defaultNodeId(value);
          }
        });
        if (_nodeId != prevNodeId) _loadAgents(); // re-probe the new target node
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
      agent: _agent,
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

    // The node the spawn will target: the explicit selection, or the sole node
    // when there's no picker. A node that lacks tmux can't spawn, so the button
    // is disabled with a hint. Unknown target (no nodes) stays enabled — the node
    // rejects it server-side if tmux is missing.
    NodeRef? selectedNode;
    if (_nodeId != null) {
      for (final n in nodes) {
        if (n.id == _nodeId) {
          selectedNode = n;
          break;
        }
      }
    } else if (nodes.length == 1) {
      selectedNode = nodes.first;
    }
    final spawnable = selectedNode?.spawnSupported ?? true;

    final allProjects = ref.watch(historyProjectsProvider).value ??
        const <HistoryProject>[];
    // Dedupe by cwd: the same path can appear on multiple nodes (or repeat in
    // history), and the dropdown asserts one item per value.
    final seenCwds = <String>{};
    final projects = [
      for (final p in allProjects)
        if (_nodeId == null || (p.nodeId ?? '') == _nodeId)
          if (seenCwds.add(p.cwd)) p,
    ];
    // Drop a stale selection that no longer maps to an item, else the dropdown
    // has zero matches for its value and asserts.
    if (_selectedCwd != null && !seenCwds.contains(_selectedCwd)) {
      _selectedCwd = null;
    }
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
                  DropdownMenuItem(
                    value: n.id,
                    enabled: n.spawnSupported,
                    child: Text(
                      n.spawnSupported ? n.label : '${n.label} (no tmux)',
                    ),
                  ),
              ],
              onChanged: (v) {
                setState(() {
                  _nodeId = v;
                  // Reset directory selection when node changes
                  _selectedCwd = null;
                  _customCwd = false;
                  _customPath = '';
                  _agents = null;
                  _agent = null;
                });
                _loadAgents();
              },
            ),
          if (_agents != null && _agents!.length >= 2)
            DropdownButton<String>(
              value: _agent,
              isExpanded: true,
              hint: const Text('Agent'),
              items: [
                for (final a in _agents!)
                  DropdownMenuItem(
                    value: a.id,
                    child:
                        Text(a.name, style: TextStyle(color: _hexColor(a.color))),
                  ),
              ],
              onChanged: (v) => setState(() => _agent = v),
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
          if (!spawnable)
            Padding(
              padding: const EdgeInsets.only(top: 8),
              child: Text(
                'tmux is not available on this node — spawning is disabled.',
                style: Theme.of(context).textTheme.bodySmall?.copyWith(
                      color: Theme.of(context).colorScheme.error,
                    ),
              ),
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
                // Block while agent probe is in flight: agent isn't known yet.
                onPressed: (busy ||
                        _prompt.trim().isEmpty ||
                        !spawnable ||
                        _agents == null)
                    ? null
                    : _spawn,
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

Color _hexColor(String hex) {
  final h = hex.startsWith('#') ? hex.substring(1) : hex;
  final v = int.tryParse(h, radix: 16);
  if (v == null || h.length != 6) return AppColors.dim;
  return Color(0xFF000000 | v);
}
