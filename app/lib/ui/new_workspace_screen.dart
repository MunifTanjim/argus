import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/misc.dart';

import '../models/project.dart';
import '../models/project_sources.dart';
import '../state/gateway.dart';
import '../state/navigation.dart';
import '../state/projects.dart';
import '../state/projects_api.dart';
import '../state/setup_text.dart';
import 'branch_picker_screen.dart';
import 'project_actions.dart';
import 'theme.dart';

enum _Tab { newBranch, branches, prs, issues }

const _tabNames = {
  _Tab.newBranch: 'New',
  _Tab.branches: 'Branches',
  _Tab.prs: 'PRs',
  _Tab.issues: 'Issues',
};

String createdText(CreateResult res) => [
  'created workspace ${baseName(res.dir)}',
  if (res.setup.isNotEmpty) 'setting up',
  if (res.warning.isNotEmpty) res.warning,
].join(' · ');

/// Reports a create, opens the new workspace when the tree lists it, and
/// offers an agent for an issue.
Future<void> afterCreate(
  ActionContext ax,
  ProjectNode p,
  CreateRequest req,
  CreateResult res,
) async {
  ax.say(createdText(res));
  ax.container.read(pendingScopeProvider.notifier).state = res.workspaceId;
  // A project.changed can arrive before the reply; this load finds the
  // workspace either way.
  ax.container
      .read(projectsProvider.notifier)
      .load(ax.container.read(gatewayProvider)?.client);
  if (req.source != 'issue' || res.prompt.isEmpty) return;
  final yes = await showDialog<bool>(
    context: ax.navigator.context,
    builder: (ctx) => AlertDialog(
      content: const Text('Start an agent with this issue?'),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(false),
          child: const Text('Not now'),
        ),
        FilledButton(
          onPressed: () => Navigator.of(ctx).pop(true),
          child: const Text('Start'),
        ),
      ],
    ),
  );
  if (yes != true) return;
  await spawnIn(
    ax,
    p,
    WorkspaceNode(id: res.workspaceId, dir: res.dir),
    prompt: res.prompt,
  );
}

class NewWorkspaceScreen extends ConsumerStatefulWidget {
  const NewWorkspaceScreen({super.key, required this.project});

  final ProjectNode project;

  @override
  ConsumerState<NewWorkspaceScreen> createState() => _NewWorkspaceScreenState();
}

class _NewWorkspaceScreenState extends ConsumerState<NewWorkspaceScreen> {
  var _tab = _Tab.newBranch;
  final _opened = <_Tab>{_Tab.newBranch};
  final _name = TextEditingController();
  final _filter = TextEditingController();
  var _target = '';
  var _creating = false;
  String? _error;

  ProjectNode get _p => widget.project;

  @override
  void dispose() {
    _name.dispose();
    _filter.dispose();
    super.dispose();
  }

  ProviderListenable<AsyncValue<Object>>? _source(_Tab t) => switch (t) {
    _Tab.branches => projectBranchesProvider(_p.id),
    _Tab.prs => projectPrsProvider(_p.id),
    _Tab.issues => projectIssuesProvider(_p.id),
    _Tab.newBranch => null,
  };

  void _retry(_Tab t) => switch (t) {
    _Tab.branches => ref.invalidate(projectBranchesProvider(_p.id)),
    _Tab.prs => ref.invalidate(projectPrsProvider(_p.id)),
    _Tab.issues => ref.invalidate(projectIssuesProvider(_p.id)),
    _Tab.newBranch => null,
  };

  void _switch(_Tab t) {
    final src = _source(t);
    if (src != null && _opened.contains(t) && ref.read(src).hasError) {
      _retry(t);
    }
    setState(() {
      _tab = t;
      _opened.add(t);
      _error = null;
      _filter.clear();
    });
  }

  String get _targetLabel {
    if (_tab == _Tab.prs) return 'from PR base';
    if (_target.isNotEmpty) return _target;
    return _p.defaultBranch.isNotEmpty ? _p.defaultBranch : 'default branch';
  }

  Future<void> _pickTarget() async {
    final b = await pickBranch(
      Navigator.of(context),
      projectId: _p.id,
      title: 'Target branch',
      current: _target,
    );
    if (b != null && mounted) setState(() => _target = b);
  }

  Future<void> _create(CreateRequest req) async {
    if (_creating) return;
    final ax = ActionContext.of(context);
    final route = ModalRoute.of(context);
    setState(() {
      _creating = true;
      _error = null;
    });
    final CreateResult res;
    try {
      res = await ax.api.create(_p.id, req);
    } catch (e) {
      // After Back the page stays mounted through its exit animation.
      if (route?.isCurrent ?? false) {
        setState(() {
          _creating = false;
          _error = actionError(e);
        });
      } else {
        ax.say('create workspace failed: ${actionError(e)}');
      }
      return;
    }
    if (route?.isCurrent ?? false) ax.navigator.pop();
    await afterCreate(ax, _p, req, res);
  }

  CreateRequest _req({
    required String source,
    String branch = '',
    int number = 0,
  }) => CreateRequest(
    source: source,
    branch: branch,
    number: number,
    targetBranch: _target,
  );

  @override
  Widget build(BuildContext context) {
    // Watching every opened tab keeps its data while the page is open.
    final data = <_Tab, AsyncValue<Object>>{
      for (final t in _opened)
        if (_source(t) case final src?) t: ref.watch(src),
    };
    return Scaffold(
      appBar: AppBar(title: Text('New workspace in ${_p.name}')),
      body: SafeArea(
        top: false,
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (_p.setupScript.isNotEmpty)
              Padding(
                padding: const EdgeInsets.fromLTRB(16, 8, 16, 0),
                child: Text(
                  'setup: ${commandLine(_p.setupScript)}',
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(color: AppColors.dim, fontSize: 12),
                ),
              ),
            Padding(
              padding: const EdgeInsets.all(8),
              child: SegmentedButton<_Tab>(
                showSelectedIcon: false,
                segments: [
                  for (final t in _Tab.values)
                    ButtonSegment(value: t, label: Text(_tabNames[t]!)),
                ],
                selected: {_tab},
                onSelectionChanged: _creating ? null : (s) => _switch(s.single),
              ),
            ),
            ListTile(
              key: const Key('create-target'),
              enabled: !_creating && _tab != _Tab.prs,
              title: Text('Target: $_targetLabel'),
              trailing: const Icon(Icons.chevron_right),
              onTap: _pickTarget,
            ),
            if (_error != null)
              Padding(
                padding: const EdgeInsets.symmetric(
                  horizontal: 16,
                  vertical: 4,
                ),
                child: Text(
                  _error!,
                  style: const TextStyle(color: AppColors.error),
                ),
              ),
            Expanded(
              child: _creating
                  ? const Center(
                      child: Row(
                        mainAxisSize: MainAxisSize.min,
                        children: [
                          SizedBox.square(
                            dimension: 18,
                            child: CircularProgressIndicator(strokeWidth: 2),
                          ),
                          SizedBox(width: 12),
                          Text('Creating…'),
                        ],
                      ),
                    )
                  : _body(data),
            ),
          ],
        ),
      ),
    );
  }

  Widget _filterField() => Padding(
    padding: const EdgeInsets.symmetric(horizontal: 8),
    child: TextField(
      key: const Key('create-filter'),
      controller: _filter,
      decoration: const InputDecoration(
        hintText: 'Filter',
        prefixIcon: Icon(Icons.search),
      ),
      onChanged: (_) => setState(() => _error = null),
    ),
  );

  Widget _body(Map<_Tab, AsyncValue<Object>> data) {
    switch (_tab) {
      case _Tab.newBranch:
        final name = _name.text.trim();
        return Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              TextField(
                key: const Key('create-branch'),
                controller: _name,
                decoration: const InputDecoration(labelText: 'Branch name'),
                onChanged: (_) => setState(() => _error = null),
              ),
              const SizedBox(height: 12),
              FilledButton(
                onPressed: name.isEmpty
                    ? null
                    : () => _create(_req(source: 'new', branch: name)),
                child: const Text('Create'),
              ),
            ],
          ),
        );
      case _Tab.branches:
        return Column(
          children: [
            _filterField(),
            Expanded(
              child: BranchListView(
                branches: data[_Tab.branches]!.whenData(
                  (v) => v as List<BranchInfo>,
                ),
                filter: _filter.text,
                disableInUse: true,
                onPick: (b) => _create(_req(source: 'branch', branch: b)),
                onRetry: () => _retry(_Tab.branches),
              ),
            ),
          ],
        );
      case _Tab.prs:
        return _sourceList<PrInfo>(
          data[_Tab.prs]!.whenData((v) => v as SourceList<PrInfo>),
          filter: filterPrs,
          title: (pr) => '#${pr.number} ${pr.title}',
          subtitle: (pr) =>
              '${pr.author} · ${pr.headBranch} → ${pr.baseBranch}',
          onPick: (pr) => _create(_req(source: 'pr', number: pr.number)),
          tab: _Tab.prs,
        );
      case _Tab.issues:
        return _sourceList<IssueInfo>(
          data[_Tab.issues]!.whenData((v) => v as SourceList<IssueInfo>),
          filter: filterIssues,
          title: (i) => '#${i.number} ${i.title}',
          subtitle: (i) => i.author,
          onPick: (i) => _create(_req(source: 'issue', number: i.number)),
          tab: _Tab.issues,
        );
    }
  }

  Widget _sourceList<T>(
    AsyncValue<SourceList<T>> async, {
    required List<T> Function(List<T>, String) filter,
    required String Function(T) title,
    required String Function(T) subtitle,
    required ValueChanged<T> onPick,
    required _Tab tab,
  }) => Column(
    children: [
      _filterField(),
      Expanded(
        child: async.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (e, _) =>
              ListNotice(text: actionError(e), onRetry: () => _retry(tab)),
          data: (list) {
            if (list.items.isEmpty) return const ListNotice(text: 'None open');
            final shown = filter(list.items, _filter.text);
            return Column(
              children: [
                if (list.truncated)
                  Padding(
                    padding: const EdgeInsets.all(8),
                    child: Text(
                      'First ${list.items.length} · the filter searches only these',
                      style: const TextStyle(
                        color: AppColors.dim,
                        fontSize: 12,
                      ),
                    ),
                  ),
                Expanded(
                  child: shown.isEmpty
                      ? const ListNotice(text: 'No matches')
                      : ListView(
                          children: [
                            for (final x in shown)
                              ListTile(
                                title: Text(
                                  title(x),
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                ),
                                subtitle: Text(
                                  subtitle(x),
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                ),
                                onTap: () => onPick(x),
                              ),
                          ],
                        ),
                ),
              ],
            );
          },
        ),
      ),
    ],
  );
}
