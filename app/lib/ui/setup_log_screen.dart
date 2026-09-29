import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/project.dart';
import '../state/projects.dart';
import '../state/projects_api.dart';
import '../state/setup_text.dart';
import 'branch_picker_screen.dart';
import 'code_block.dart';
import 'project_actions.dart';

class SetupLogScreen extends ConsumerStatefulWidget {
  const SetupLogScreen({super.key, required this.workspaceId});

  final String workspaceId;

  @override
  ConsumerState<SetupLogScreen> createState() => _SetupLogScreenState();
}

class _SetupLogScreenState extends ConsumerState<SetupLogScreen> {
  String? _output;
  Object? _error;
  Timer? _timer;
  var _live = false;
  var _inFlight = false;
  var _seq = 0;

  SetupRun? _run(ProjectsState s) =>
      lookupWorkspace(s.projects, widget.workspaceId)?.$2.setup;

  bool _running(ProjectsState s) => _run(s)?.state == 'running';

  @override
  void initState() {
    super.initState();
    _fetch();
    if (_running(ref.read(projectsProvider))) _poll(true);
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  // Like the TUI, the next poll waits for the last reply, so a slow link
  // never stacks fetches.
  void _poll(bool on) {
    _live = on;
    if (!on) {
      _timer?.cancel();
      _timer = null;
    }
    _schedule();
  }

  void _schedule() {
    if (!_live || _inFlight || _timer != null || _error != null) return;
    _timer = Timer(const Duration(seconds: 1), () {
      _timer = null;
      _fetch();
    });
  }

  Future<void> _fetch() async {
    final seq = ++_seq;
    _inFlight = true;
    try {
      final out = await ref
          .read(projectsApiProvider)
          .setupLog(widget.workspaceId);
      if (!mounted || seq != _seq) return;
      setState(() {
        _output = out;
        _error = null;
      });
    } catch (e) {
      if (!mounted || seq != _seq) return;
      setState(() => _error = e);
    } finally {
      if (seq == _seq) _inFlight = false;
    }
    if (mounted) _schedule();
  }

  void _retry() {
    setState(() => _error = null);
    _fetch();
    if (_running(ref.read(projectsProvider))) _poll(true);
  }

  // The tree flips to running when the rerun starts; the listener then polls.
  Future<void> _rerun(WorkspaceNode w) async {
    await runSetup(ActionContext.of(context), w);
    if (!mounted) return;
    await _fetch();
  }

  void _onProjects(ProjectsState? prev, ProjectsState next) {
    final was = prev != null && _running(prev);
    final now = _running(next);
    if (now && !was) _poll(true);
    if (was && !now) _poll(false);
    // A run that ended, or changed while this page was idle, has new output.
    final before = prev == null ? null : _run(prev);
    final after = _run(next);
    if (!now &&
        (was ||
            before?.state != after?.state ||
            before?.exitCode != after?.exitCode ||
            before?.outputTail != after?.outputTail)) {
      _fetch();
    }
  }

  @override
  Widget build(BuildContext context) {
    ref.listen<ProjectsState>(projectsProvider, _onProjects);
    final hit = lookupWorkspace(
      ref.watch(projectsProvider).projects,
      widget.workspaceId,
    );
    final output = _output == null ? null : cleanOutput(_output!);
    return Scaffold(
      appBar: AppBar(
        title: Text(hit == null ? 'Setup log' : 'Setup log · ${hit.$2.name}'),
        actions: [
          if (hit != null && hit.$1.setupScript.isNotEmpty)
            IconButton(
              icon: const Icon(Icons.replay),
              tooltip: 'Rerun setup',
              onPressed: () => _rerun(hit.$2),
            ),
        ],
      ),
      body: SafeArea(
        top: false,
        child: switch ((output, _error)) {
          (_, final Object e) => ListNotice(
            text: actionError(e),
            onRetry: _retry,
          ),
          (null, _) => const Center(child: CircularProgressIndicator()),
          (final String o, _) when o.trim().isEmpty => const ListNotice(
            text: 'No output.',
          ),
          (final String o, _) => SingleChildScrollView(
            padding: const EdgeInsets.all(12),
            child: codeView(o, lineNumberToggle: false, prettyJson: false),
          ),
        },
      ),
    );
  }
}
