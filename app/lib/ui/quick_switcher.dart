import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/enums.dart';
import '../models/project.dart';
import '../models/session.dart';
import '../state/navigation.dart';
import '../state/projects.dart';
import '../state/push.dart';
import '../state/sessions.dart';
import 'theme.dart';

sealed class SwitcherEntry {
  String get title;
  String get subtitle;

  /// What the query is matched against, lowercase-insensitively.
  Iterable<String> get _haystack;
}

class SessionEntry extends SwitcherEntry {
  SessionEntry(this.session);

  final Session session;

  @override
  String get title => session.displayTitle;

  @override
  String get subtitle => [
    if (session.repo?.isNotEmpty ?? false) session.repo!,
    if (session.branch?.isNotEmpty ?? false) session.branch!,
  ].join(' / ');

  @override
  Iterable<String> get _haystack => [
    title,
    session.repo ?? '',
    session.branch ?? '',
  ];
}

class WorkspaceEntry extends SwitcherEntry {
  WorkspaceEntry(this.project, this.workspace);

  final ProjectNode project;
  final WorkspaceNode workspace;

  @override
  String get title => '${project.name} · ${workspace.name}';

  @override
  String get subtitle => workspace.branch;

  @override
  Iterable<String> get _haystack => [title, project.name, workspace.branch];
}

List<SwitcherEntry> switcherEntries(
  Iterable<Session> sessions,
  List<ProjectNode> projects,
  String query,
) {
  final live = sessions.where((s) => s.status != SessionStatus.dead).toList()
    ..sort((a, b) {
      final x = a.statusSince, y = b.statusSince;
      if (x == null && y == null) return 0;
      if (x == null) return 1;
      if (y == null) return -1;
      return y.compareTo(x);
    });
  final all = <SwitcherEntry>[
    for (final s in live) SessionEntry(s),
    for (final p in projects)
      for (final w in p.workspaces)
        if (!w.isGone) WorkspaceEntry(p, w),
  ];
  final q = query.trim().toLowerCase();
  if (q.isEmpty) return all;
  return [
    for (final e in all)
      if (e._haystack.any((h) => h.toLowerCase().contains(q))) e,
  ];
}

Future<void> showQuickSwitcher(BuildContext context) =>
    showDialog<void>(context: context, builder: (_) => const _QuickSwitcher());

class _QuickSwitcher extends ConsumerStatefulWidget {
  const _QuickSwitcher();

  @override
  ConsumerState<_QuickSwitcher> createState() => _QuickSwitcherState();
}

class _QuickSwitcherState extends ConsumerState<_QuickSwitcher> {
  String _query = '';
  int _selected = 0;

  List<SwitcherEntry> _entries() => switcherEntries(
    ref.read(sessionsProvider).values,
    ref.read(projectsProvider).projects,
    _query,
  );

  void _move(int delta) {
    final n = _entries().length;
    if (n == 0) return;
    setState(() => _selected = (_selected + delta).clamp(0, n - 1));
  }

  void _open(SwitcherEntry e) {
    Navigator.of(context).pop();
    switch (e) {
      case SessionEntry(:final session):
        ref.read(pendingOpenSessionProvider.notifier).state = PendingOpen(
          session.id,
        );
      case WorkspaceEntry(:final workspace):
        ref.read(scopeProvider.notifier).state = workspace.id;
    }
  }

  KeyEventResult _onKey(FocusNode _, KeyEvent event) {
    if (event is KeyUpEvent) return KeyEventResult.ignored;
    final key = event.logicalKey;
    if (key == LogicalKeyboardKey.arrowDown) {
      _move(1);
    } else if (key == LogicalKeyboardKey.arrowUp) {
      _move(-1);
    } else if (key == LogicalKeyboardKey.enter ||
        key == LogicalKeyboardKey.numpadEnter) {
      final es = _entries();
      if (es.isNotEmpty) _open(es[_selected.clamp(0, es.length - 1)]);
    } else if (key == LogicalKeyboardKey.escape) {
      Navigator.of(context).pop();
    } else {
      return KeyEventResult.ignored;
    }
    return KeyEventResult.handled;
  }

  @override
  Widget build(BuildContext context) {
    ref.watch(sessionsProvider);
    ref.watch(projectsProvider);
    final entries = _entries();
    final selected = entries.isEmpty
        ? 0
        : _selected.clamp(0, entries.length - 1);
    return Dialog(
      alignment: Alignment.topCenter,
      insetPadding: const EdgeInsets.only(top: 80, left: 24, right: 24),
      child: ConstrainedBox(
        constraints: const BoxConstraints(maxWidth: 560, maxHeight: 480),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Focus(
              onKeyEvent: _onKey,
              child: TextField(
                key: const Key('quick-switcher-field'),
                autofocus: true,
                decoration: const InputDecoration(
                  hintText: 'Jump to a session or workspace',
                  prefixIcon: Icon(Icons.search, size: 18),
                  border: InputBorder.none,
                ),
                onChanged: (v) => setState(() {
                  _query = v;
                  _selected = 0;
                }),
              ),
            ),
            const Divider(height: 1),
            Flexible(
              child: entries.isEmpty
                  ? const Padding(
                      padding: EdgeInsets.all(16),
                      child: Text(
                        'No matches',
                        style: TextStyle(color: AppColors.dim),
                      ),
                    )
                  : ListView.builder(
                      shrinkWrap: true,
                      itemCount: entries.length,
                      itemBuilder: (_, i) {
                        final e = entries[i];
                        return ListTile(
                          dense: true,
                          selected: i == selected,
                          selectedTileColor: Theme.of(
                            context,
                          ).colorScheme.primary.withValues(alpha: 0.12),
                          leading: Icon(
                            e is SessionEntry
                                ? Icons.terminal
                                : Icons.account_tree_outlined,
                            size: 18,
                          ),
                          title: Text(
                            e.title,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                          ),
                          subtitle: e.subtitle.isEmpty
                              ? null
                              : Text(
                                  e.subtitle,
                                  maxLines: 1,
                                  overflow: TextOverflow.ellipsis,
                                ),
                          onTap: () => _open(e),
                        );
                      },
                    ),
            ),
          ],
        ),
      ),
    );
  }
}
