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
import 'relative_time.dart';
import 'theme.dart';
import 'voice_input_field.dart';

/// A fixed place to spawn in, such as a workspace: the dialog hides its node
/// and directory pickers.
class SpawnTarget {
  const SpawnTarget({
    required this.nodeId,
    required this.cwd,
    required this.label,
    this.prompt = '',
  });

  final String nodeId;
  final String cwd;
  final String label;
  final String prompt;
}

Route<void> spawnRoute({SpawnTarget? target}) => MaterialPageRoute(
      fullscreenDialog: true,
      builder: (_) => SpawnDialog(target: target),
    );

class SpawnDialog extends StatelessWidget {
  const SpawnDialog({super.key, this.target});

  final SpawnTarget? target;

  @override
  Widget build(BuildContext context) => Scaffold(
        appBar: AppBar(title: const Text('New session')),
        body: SafeArea(child: SpawnDialogBody(target: target)),
      );
}

class SpawnDialogBody extends ConsumerStatefulWidget {
  const SpawnDialogBody({super.key, this.target});

  final SpawnTarget? target;

  @override
  ConsumerState<SpawnDialogBody> createState() => _SpawnDialogBodyState();
}

class _SpawnDialogBodyState extends ConsumerState<SpawnDialogBody> {
  late final SpawnViewModel _vm;
  // Nodes from server.info; preferred over session-derived nodes so a target is
  // offered even with zero sessions. Empty until the call returns.
  List<NodeRef> _remoteNodes = const [];
  String? _nodeId;
  late String _prompt = widget.target?.prompt ?? '';
  // Dictation writes into the field directly, so the prompt needs a controller
  // as well as the mirrored _prompt the Spawn button gates on.
  late final _promptCtl = TextEditingController(text: _prompt);

  // null while probing; picker shows only when >=2.
  List<AgentInfo>? _agents;
  String? _agent;

  // null while loading; empty hides the picker.
  List<ModelInfo>? _models;
  bool _modelsFailed = false;
  String? _model; // null = the agent's default

  // Directory state
  String? _selectedCwd; // resolved when projects load or the user picks
  bool _customCwd = false;
  final _pathCtl = TextEditingController();
  final _pathFocus = FocusNode();
  int _completeCalls = 0; // only the latest call asks the node
  (String, List<String>)? _seeded; // the home and its children, from the seed

  @override
  void initState() {
    super.initState();
    _vm = SpawnViewModel(ref.read(sessionRepositoryProvider));
    _vm.spawn.addListener(_onCommand);
    final target = widget.target;
    if (target != null && target.nodeId.isNotEmpty) {
      _nodeId = target.nodeId;
    } else {
      final nodes = nodesFromSessions(ref.read(sessionsProvider).values);
      if (nodes.length >= 2) {
        _nodeId = _defaultNodeId(nodes);
      }
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
    _loadModels();
  }

  // Last call wins if the node or agent changes mid-load; the model resets to
  // Default so one agent's alias is never sent to another.
  Future<void> _loadModels() async {
    final nodeId = _nodeId;
    final agent = _agent;
    setState(() {
      _models = agent == null ? const [] : null;
      _modelsFailed = false;
      _model = null;
    });
    if (agent == null) return;
    final result =
        await ref.read(sessionRepositoryProvider).listModels(nodeId, agent);
    if (!mounted || nodeId != _nodeId || agent != _agent) return;
    switch (result) {
      case Ok(:final value):
        setState(() => _models = value);
      case Error():
        setState(() {
          _models = const [];
          _modelsFailed = true;
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
          if (widget.target == null &&
              value.length >= 2 &&
              (_nodeId == null || !value.any((n) => n.id == _nodeId))) {
            _nodeId = _defaultNodeId(value);
          }
          if (_nodeId != prevNodeId) _clearCustomPath(); // another node's path
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
    _promptCtl.dispose();
    _pathCtl.dispose();
    _pathFocus.dispose();
    super.dispose();
  }

  String? _effectiveCwd() {
    final target = widget.target;
    if (target != null) return target.cwd;
    final cwd = _customCwd ? _pathCtl.text.trim() : (_selectedCwd ?? '').trim();
    return cwd.isEmpty ? null : cwd;
  }

  Future<void> _spawn() async {
    await _vm.spawn.execute(SpawnRequest(
      nodeId: _nodeId,
      cwd: _effectiveCwd(),
      agent: _agent,
      model: _model,
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

    final history = ref.watch(historyProjectsProvider);
    final allProjects = history.value ?? const <HistoryProject>[];
    // Dedupe by cwd: the same path can appear on multiple nodes or repeat in
    // history.
    final seenCwds = <String>{};
    final projects = [
      for (final p in allProjects)
        if (!p.missing && (_nodeId == null || (p.nodeId ?? '') == _nodeId))
          if (seenCwds.add(p.cwd)) p,
    ];
    // Drop a stale selection from another node's list.
    if (_selectedCwd != null && !seenCwds.contains(_selectedCwd)) {
      _selectedCwd = null;
    }
    _selectedCwd ??= projects.isNotEmpty ? projects.first.cwd : null;

    final target = widget.target;
    // With no directory to offer, go straight to a custom path.
    if (target == null && history.hasValue && projects.isEmpty && !_customCwd) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _useCustomPath());
    }
    final agents = _agents;
    final agent = agents?.where((a) => a.id == _agent).firstOrNull;
    final project = projects.where((p) => p.cwd == _selectedCwd).firstOrNull;
    final models = _models;
    final model = models?.where((m) => m.id == _model).firstOrNull;

    final page = SingleChildScrollView(
      padding: const EdgeInsets.all(16),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (target == null && nodes.length >= 2)
            _PickerField(
              label: 'Node',
              value: selectedNode?.label ?? 'Choose a node',
              onTap: busy ? null : () => _pickNode(nodes),
            ),
          if (agents != null && agents.length >= 2)
            _PickerField(
              label: 'Agent',
              value: agent?.name ?? 'Choose an agent',
              valueColor: agent == null ? null : _hexColor(agent.color),
              onTap: busy ? null : () => _pickAgent(agents),
            ),
          if (_agent != null &&
              (models == null || models.isNotEmpty || _modelsFailed))
            _PickerField(
              label: 'Model',
              value: model?.name ?? 'Default',
              detail: models == null
                  ? 'Loading models\u2026'
                  : _modelsFailed
                      ? "Couldn't load models"
                      : null,
              onTap: busy ? null : () => _pickModel(models ?? const []),
            ),
          if (target != null)
            _PickerField(
              label: 'Directory',
              value: target.label,
              detail: target.cwd,
            )
          else
            _PickerField(
              label: 'Directory',
              value: _customCwd
                  ? 'Custom path'
                  : project?.label ?? 'Choose a directory',
              detail: _customCwd ? null : project?.cwd,
              onTap: busy ? null : () => _pickDirectory(projects),
            ),
          if (target == null && _customCwd)
            Padding(
              padding: const EdgeInsets.only(bottom: 16),
              child: Autocomplete<String>(
                textEditingController: _pathCtl,
                focusNode: _pathFocus,
                optionsBuilder: (v) => _completePath(v.text),
                onSelected: (_) => setState(() {}),
                fieldViewBuilder: (context, controller, focusNode, onSubmit) =>
                    TextField(
                  key: const Key('spawn-custom-path'),
                  controller: controller,
                  focusNode: focusNode,
                  autofocus: true,
                  autocorrect: false,
                  enableSuggestions: false,
                  decoration: const InputDecoration(
                    labelText: 'Path',
                    hintText: '/path/to/project',
                  ),
                  onChanged: (_) => setState(() {}),
                  onSubmitted: (_) => onSubmit(),
                ),
              ),
            ),
          VoiceInputField(
            controller: _promptCtl,
            onChanged: (v) => setState(() => _prompt = v),
            statusBelow: true,
            field: (suffixIcon) => TextField(
              key: const Key('spawn-prompt'),
              controller: _promptCtl,
              decoration: InputDecoration(
                labelText: 'Prompt',
                hintText: 'What should this session work on?',
                suffixIcon: suffixIcon,
              ),
              minLines: 3,
              maxLines: null,
              keyboardType: TextInputType.multiline,
              textCapitalization: TextCapitalization.sentences,
              onChanged: (v) => setState(() => _prompt = v),
            ),
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
          const SizedBox(height: 24),
          FilledButton(
            // Block while agent probe is in flight: agent isn't known yet.
            onPressed:
                (busy || _prompt.trim().isEmpty || !spawnable || agents == null)
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
    );
    // Leaving mid-spawn would drop its outcome.
    return PopScope(canPop: !busy, child: page);
  }

  Future<void> _pickNode(List<NodeRef> nodes) async {
    final v = await _pick(
      title: 'Node',
      selected: _nodeId,
      choices: [
        for (final n in nodes)
          _Choice(
            value: n.id,
            title: n.label,
            subtitle: n.spawnSupported ? null : 'no tmux',
            enabled: n.spawnSupported,
          ),
      ],
    );
    if (v == null || v == _nodeId) return;
    setState(() {
      _nodeId = v;
      _selectedCwd = null;
      _clearCustomPath();
      _agents = null;
      _agent = null;
    });
    _loadAgents();
  }

  Future<void> _pickAgent(List<AgentInfo> agents) async {
    final v = await _pick(
      title: 'Agent',
      selected: _agent,
      choices: [
        for (final a in agents)
          _Choice(value: a.id, title: a.name, color: _hexColor(a.color)),
      ],
    );
    if (v == null || v == _agent) return;
    setState(() => _agent = v);
    _loadModels();
  }

  Future<void> _pickModel(List<ModelInfo> models) async {
    final nodeId = _nodeId;
    final agent = _agent;
    final v = await _pick(
      title: 'Model',
      selected: _model ?? _defaultModel,
      filterHint: 'Filter models',
      choices: [
        const _Choice(value: _defaultModel, title: 'Default'),
        for (final m in models) _Choice(value: m.id, title: m.name),
      ],
    );
    // The list was for this node and agent; drop a pick made after either changed.
    if (v == null || !mounted || nodeId != _nodeId || agent != _agent) return;
    setState(() => _model = v == _defaultModel ? null : v);
  }

  Future<void> _pickDirectory(List<HistoryProject> projects) async {
    final v = await _pick(
      title: 'Directory',
      selected: _customCwd ? _customValue : _selectedCwd,
      filterHint: 'Filter by name or path',
      choices: [
        for (final p in projects)
          _Choice(
            value: p.cwd,
            title: p.label,
            subtitle: p.cwd,
            trailing: relativeTime(p.lastActivity),
          ),
      ],
      footer: const _Choice(value: _customValue, title: 'Custom path…'),
    );
    if (v == null) return;
    if (v == _customValue) return _useCustomPath();
    setState(() {
      _customCwd = false;
      _selectedCwd = v;
    });
  }

  void _clearCustomPath() {
    _customCwd = false;
    _pathCtl.clear();
    _seeded = null;
  }

  void _useCustomPath() {
    if (!mounted || _customCwd) return;
    setState(() => _customCwd = true);
    _seedHome();
  }

  Future<void> _seedHome() async {
    final nodeId = _nodeId;
    final result =
        await ref.read(sessionRepositoryProvider).completeDir(nodeId, '');
    if (!mounted || nodeId != _nodeId || !_customCwd) return;
    if (result case Ok(:final value)
        when value.home.isNotEmpty && _pathCtl.text.isEmpty) {
      final home = value.home.endsWith('/') ? value.home : '${value.home}/';
      _seeded = (home, value.paths);
      _pathCtl.value = TextEditingValue(
        text: home,
        selection: TextSelection.collapsed(offset: home.length),
      );
      setState(() {});
    }
  }

  // Asks only once [path] has stayed unchanged for [_completeDebounce].
  Future<Iterable<String>> _completePath(String path) async {
    final call = ++_completeCalls;
    if (_seeded case (final home, final paths) when path == home) return paths;
    await Future<void>.delayed(_completeDebounce);
    if (!mounted || call != _completeCalls || path.isEmpty) return const [];
    final result =
        await ref.read(sessionRepositoryProvider).completeDir(_nodeId, path);
    if (result case Ok(:final value)) return value.paths;
    return const [];
  }

  Future<String?> _pick({
    required String title,
    required List<_Choice> choices,
    String? selected,
    String? filterHint,
    _Choice? footer,
  }) =>
      Navigator.of(context).push<String>(
        MaterialPageRoute(
          builder: (_) => _ChoiceScreen(
            title: title,
            choices: choices,
            selected: selected,
            filterHint: filterHint,
            footer: footer,
          ),
        ),
      );
}

const _customValue = '__custom__';
const _defaultModel = '__default__';

const _completeDebounce = Duration(milliseconds: 300);

/// Read-only when [onTap] is null.
class _PickerField extends StatelessWidget {
  const _PickerField({
    required this.label,
    required this.value,
    this.detail,
    this.valueColor,
    this.onTap,
  });

  final String label;
  final String value;
  final String? detail;
  final Color? valueColor;
  final VoidCallback? onTap;

  @override
  Widget build(BuildContext context) => Padding(
        padding: const EdgeInsets.only(bottom: 16),
        child: InkWell(
          onTap: onTap,
          child: InputDecorator(
            decoration: InputDecoration(
              labelText: label,
              suffixIcon: onTap == null ? null : const Icon(Icons.chevron_right),
            ),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              mainAxisSize: MainAxisSize.min,
              children: [
                Text(value, style: TextStyle(color: valueColor)),
                if (detail != null && detail!.isNotEmpty)
                  Text(
                    detail!,
                    style: const TextStyle(color: AppColors.dim, fontSize: 12),
                  ),
              ],
            ),
          ),
        ),
      );
}

class _Choice {
  const _Choice({
    required this.value,
    required this.title,
    this.subtitle,
    this.trailing,
    this.color,
    this.enabled = true,
  });

  final String value;
  final String title;
  final String? subtitle;
  final String? trailing;
  final Color? color;
  final bool enabled;
}

/// A full-page list of choices; pops with the picked value. Shows a filter
/// over title and subtitle when [filterHint] is set; [footer] is never
/// filtered out.
class _ChoiceScreen extends StatefulWidget {
  const _ChoiceScreen({
    required this.title,
    required this.choices,
    this.selected,
    this.filterHint,
    this.footer,
  });

  final String title;
  final List<_Choice> choices;
  final String? selected;
  final String? filterHint;
  final _Choice? footer;

  @override
  State<_ChoiceScreen> createState() => _ChoiceScreenState();
}

class _ChoiceScreenState extends State<_ChoiceScreen> {
  var _filter = '';

  bool _matches(_Choice c) {
    final q = _filter.trim().toLowerCase();
    if (q.isEmpty) return true;
    return c.title.toLowerCase().contains(q) ||
        (c.subtitle ?? '').toLowerCase().contains(q);
  }

  Widget _tile(_Choice c) => ListTile(
        enabled: c.enabled,
        title: Text(c.title, style: TextStyle(color: c.color)),
        subtitle: c.subtitle == null ? null : Text(c.subtitle!),
        trailing: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            if (c.trailing != null && c.trailing!.isNotEmpty)
              Text(
                c.trailing!,
                style: const TextStyle(color: AppColors.dim, fontSize: 12),
              ),
            if (c.value == widget.selected)
              const Padding(
                padding: EdgeInsets.only(left: 8),
                child: Icon(Icons.check, size: 20),
              ),
          ],
        ),
        onTap: c.enabled ? () => Navigator.of(context).pop(c.value) : null,
      );

  @override
  Widget build(BuildContext context) {
    final shown = widget.choices.where(_matches).toList();
    final footer = widget.footer;
    return Scaffold(
      appBar: AppBar(title: Text(widget.title)),
      body: SafeArea(
        top: false,
        child: Column(
          children: [
            if (widget.filterHint != null)
              Padding(
                padding: const EdgeInsets.all(8),
                child: TextField(
                  key: const Key('spawn-filter'),
                  decoration: InputDecoration(
                    hintText: widget.filterHint,
                    prefixIcon: const Icon(Icons.search),
                  ),
                  onChanged: (v) => setState(() => _filter = v),
                ),
              ),
            Expanded(
              child: ListView(
                children: [
                  for (final c in shown) _tile(c),
                  if (footer != null) _tile(footer),
                ],
              ),
            ),
          ],
        ),
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
