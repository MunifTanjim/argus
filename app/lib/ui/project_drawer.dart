import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/project.dart';
import '../state/gateway.dart';
import '../transport/connection.dart';
import '../state/left_sidebar.dart';
import '../state/navigation.dart';
import '../state/project_tree.dart';
import '../state/projects.dart';
import '../state/projects_api.dart';
import '../state/sessions.dart';
import 'nerd_icon.dart';
import 'project_actions.dart';
import 'session_sections_list.dart';
import 'settings_screen.dart';
import 'shell_drawer.dart';
import 'fold_header.dart';
import 'sidebar_filter_bar.dart';
import 'sidebar_sessions_list.dart';
import 'spawn_dialog.dart';
import 'text_measure.dart';
import 'theme.dart';

class ProjectDrawer extends ConsumerStatefulWidget {
  const ProjectDrawer({super.key});

  @override
  ConsumerState<ProjectDrawer> createState() => _ProjectDrawerState();
}

class _ProjectDrawerState extends ConsumerState<ProjectDrawer> {
  // Both are created here: a lazy one first read in dispose() would use ref
  // after unmount, as when the other tab was showing the whole time.
  late final TextEditingController _filter;
  late final TextEditingController _sessionsFilter;

  @override
  void initState() {
    super.initState();
    _filter = TextEditingController(text: ref.read(treeViewProvider).filter);
    _sessionsFilter = TextEditingController(
      text: ref.read(sidebarSessionsFilterProvider),
    );
  }

  @override
  void dispose() {
    _filter.dispose();
    _sessionsFilter.dispose();
    super.dispose();
  }

  Future<void> _refresh() => ref
      .read(projectsProvider.notifier)
      .load(ref.read(gatewayProvider)?.client);

  void _close() => ShellDrawerScope.closeDrawerOf(context)?.call();

  void _select(String? workspaceId) {
    ref.read(scopeProvider.notifier).state = workspaceId;
    _close();
  }

  void _newSession() {
    final ax = ActionContext.of(context);
    _close();
    ax.navigator.push(spawnRoute());
  }

  void _openSettings() {
    _close();
    Navigator.of(
      context,
    ).push(MaterialPageRoute(builder: (_) => const SettingsScreen()));
  }

  void _showError(String name, String error) => showDialog<void>(
    context: context,
    builder: (ctx) => AlertDialog(
      title: Text(name),
      content: Text(error),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(ctx).pop(),
          child: const Text('Close'),
        ),
      ],
    ),
  );

  @override
  Widget build(BuildContext context) {
    final projects = ref.watch(projectsProvider);
    final view = ref.watch(treeViewProvider);
    final sessions = ref.watch(sessionsProvider).values;
    final scope = ref.watch(scopeProvider);
    final removing = ref.watch(removingProvider);
    final act = workspaceActivity(sessions);
    final rows = buildTreeRows(projects.projects, view);
    final tab = ref.watch(leftSidebarTabProvider);
    final conn = ref.watch(connStateProvider);

    return Material(
      color: context.tokens.surface,
      child: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            _header(tab),
            Expanded(
              child: tab == LeftSidebarTab.sessions
                  ? SidebarSessionsList(onOpen: _close)
                  : RefreshIndicator(
                      onRefresh: _refresh,
                      child: ListView(
                        padding: const EdgeInsets.only(bottom: 8),
                        children: [
                          _row(
                            selected: scope == null,
                            onTap: () => _select(null),
                            leading: const Icon(Icons.home_outlined, size: 18),
                            title: 'Home',
                            trailing: _badges(homeActivity(sessions)),
                          ),
                          if (!projects.loaded && projects.error == null)
                            const Padding(
                              key: Key('projects-loading'),
                              padding: EdgeInsets.all(12),
                              child: Center(
                                child: SizedBox.square(
                                  dimension: 18,
                                  child: CircularProgressIndicator(
                                    strokeWidth: 2,
                                  ),
                                ),
                              ),
                            ),
                          if (projects.error != null &&
                              projects.projects.isEmpty)
                            _errorRow(projects.error!),
                          for (final r in rows)
                            _treeRow(
                              r,
                              act,
                              scope,
                              projects.projects,
                              removing,
                            ),
                        ],
                      ),
                    ),
            ),
            if (conn == ConnState.connected)
              const Divider(height: 1)
            else
              ReconnectBanner(
                state: conn,
                message: ref.watch(connErrorProvider),
              ),
            _filterBar(tab, view),
          ],
        ),
      ),
    );
  }

  Widget _header(LeftSidebarTab tab) => Padding(
    padding: const EdgeInsets.fromLTRB(16, 8, 4, 4),
    child: Row(
      children: [
        Expanded(
          child: Row(
            children: [
              Flexible(child: _tab('Projects', LeftSidebarTab.projects, tab)),
              const SizedBox(width: 16),
              Flexible(child: _tab('Sessions', LeftSidebarTab.sessions, tab)),
            ],
          ),
        ),
        IconButton(
          tooltip: 'New session',
          icon: const Icon(Icons.add),
          onPressed: _newSession,
        ),
      ],
    ),
  );

  Widget _tab(String label, LeftSidebarTab value, LeftSidebarTab current) {
    final on = value == current;
    return InkWell(
      borderRadius: BorderRadius.circular(4),
      onTap: () => ref.read(leftSidebarTabProvider.notifier).set(value),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 6),
        child: Container(
          padding: const EdgeInsets.only(bottom: 2),
          decoration: BoxDecoration(
            border: Border(
              bottom: BorderSide(
                width: 2,
                color: on ? context.tokens.text : Colors.transparent,
              ),
            ),
          ),
          child: Text(
            label,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(
              fontSize: 15,
              fontWeight: on ? FontWeight.w700 : FontWeight.w500,
              color: on ? null : context.tokens.textDim,
            ),
          ),
        ),
      ),
    );
  }

  Widget _filterBar(LeftSidebarTab tab, TreeView view) {
    final projectsTab = tab == LeftSidebarTab.projects;
    final wsOnly = ref.watch(sidebarSessionsWorkspaceFilterProvider) != null;
    return SidebarFilterBar(
      key: ValueKey(tab),
      controller: projectsTab ? _filter : _sessionsFilter,
      hintText: projectsTab ? 'Filter projects' : 'Filter sessions',
      onChanged: projectsTab
          ? ref.read(treeViewProvider.notifier).setFilter
          : (q) => ref.read(sidebarSessionsFilterProvider.notifier).state = q,
      options: projectsTab
          ? [
              CheckboxMenuButton(
                value: view.showHidden,
                onChanged: (_) =>
                    ref.read(treeViewProvider.notifier).toggleHidden(),
                child: const Text('Show hidden'),
              ),
              CheckboxMenuButton(
                value: view.showGone,
                onChanged: (_) =>
                    ref.read(treeViewProvider.notifier).toggleGone(),
                child: const Text('Show gone'),
              ),
              MenuItemButton(onPressed: _refresh, child: const Text('Refresh')),
            ]
          : [
              CheckboxMenuButton(
                value: wsOnly,
                onChanged: ref.watch(shownWorkspaceProvider) == null
                    ? null
                    : (_) =>
                          ref
                                  .read(
                                    sidebarSessionsWorkspaceOnlyProvider
                                        .notifier,
                                  )
                                  .state =
                              !wsOnly,
                child: const Text('This workspace only'),
              ),
            ],
      optionsActive: projectsTab ? view.showHidden || view.showGone : wsOnly,
      onSettings: _openSettings,
    );
  }

  Widget _errorRow(String error) => Padding(
    padding: const EdgeInsets.all(12),
    child: Row(
      children: [
        Expanded(
          child: Text(
            'Could not load projects: $error',
            style: TextStyle(color: context.tokens.error, fontSize: 12),
          ),
        ),
        TextButton(onPressed: _refresh, child: const Text('Retry')),
      ],
    ),
  );

  Widget _treeRow(
    TreeRow r,
    Map<String, Activity> act,
    String? scope,
    List<ProjectNode> projects,
    Set<String> removing,
  ) {
    switch (r.kind) {
      case TreeRowKind.node:
        return FoldHeader(
          inset: true,
          label: r.label,
          folded: r.folded,
          onTap: () => ref.read(treeViewProvider.notifier).toggleFold(r.id),
          activity: sumActivity(act, r.workspaceIds),
        );
      case TreeRowKind.project:
        return _row(
          onTap: () => ref.read(treeViewProvider.notifier).toggleFold(r.id),
          onLongPress: () {
            final p = lookupProject(projects, r.id);
            if (p != null) showProjectSheet(context, p);
          },
          leading: r.hasKids
              ? Icon(
                  r.folded ? Icons.chevron_right : Icons.expand_more,
                  size: 18,
                  color: context.tokens.textDim,
                )
              : null,
          title: _title(r),
          titleStyle: TextStyle(
            fontWeight: FontWeight.w600,
            color: r.hidden ? context.tokens.textDim : null,
          ),
          titleSuffix: [
            if (r.hidden)
              Icon(
                Icons.visibility_off_outlined,
                size: 13,
                color: context.tokens.textDim,
                semanticLabel: 'hidden',
              ),
            if (r.pinned)
              Icon(Icons.push_pin, size: 13, color: context.tokens.textDim),
          ],
          trailing: _trailing([
            if (r.gitError.isNotEmpty)
              InkWell(
                borderRadius: BorderRadius.circular(12),
                onTap: () => _showError(r.label, r.gitError),
                child: Padding(
                  padding: const EdgeInsets.all(4),
                  child: Icon(
                    Icons.warning_amber,
                    size: 16,
                    color: context.tokens.error,
                    semanticLabel: 'Git error',
                  ),
                ),
              ),
            if (r.folded) ?_badges(sumActivity(act, r.workspaceIds)),
          ]),
        );
      case TreeRowKind.workspace:
        return _row(
          indent: _col,
          selected: scope == r.id,
          onTap: () => _select(r.id),
          onLongPress: () {
            final hit = lookupWorkspace(projects, r.id);
            if (hit != null) showWorkspaceSheet(context, hit.$1, hit.$2);
          },
          leading: r.isMain
              ? Icon(
                  Icons.folder_outlined,
                  size: 16,
                  color: context.tokens.textDim,
                  semanticLabel: 'main worktree',
                )
              : GitBranchIcon(size: 16, color: context.tokens.textDim),
          title: _title(r),
          detail: r.detail != r.label ? r.detail : null,
          trailing: _trailing([
            if (removing.contains(r.id))
              Text(
                'removing…',
                style: TextStyle(color: context.tokens.textDim, fontSize: 12),
              ),
            if (r.setup == SetupMark.running)
              Icon(
                Icons.sync,
                size: 14,
                color: context.tokens.busy,
                semanticLabel: 'setup running',
              ),
            if (r.setup == SetupMark.failed)
              Icon(
                Icons.error_outline,
                size: 14,
                color: context.tokens.error,
                semanticLabel: 'setup failed',
              ),
            ?_badges(sumActivity(act, r.workspaceIds)),
          ]),
        );
    }
  }

  static String _title(TreeRow r) => r.gone ? '${r.label} (gone)' : r.label;

  Widget? _trailing(List<Widget> children) => children.isEmpty
      ? null
      : Row(mainAxisSize: MainAxisSize.min, children: children);

  Widget? _badges(Activity a) => a.live > 0 ? ActivityBadges(a) : null;

  // The leading icon column plus its gap. A workspace is indented by one
  // column, so its icon sits under its project's name.
  static const _col = 24.0;
  static const _icon = 18.0;

  Widget _row({
    required String title,
    required VoidCallback onTap,
    VoidCallback? onLongPress,
    Widget? leading,
    TextStyle? titleStyle,
    List<Widget> titleSuffix = const [],
    String? detail,
    Widget? trailing,
    bool selected = false,
    double indent = 0,
  }) {
    final titleText = Text(
      title,
      maxLines: 1,
      overflow: TextOverflow.ellipsis,
      style: const TextStyle(fontSize: 14).merge(titleStyle),
    );
    return Padding(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 1),
      child: Material(
        color: selected ? context.tokens.surfaceRaised : Colors.transparent,
        borderRadius: BorderRadius.circular(context.tokens.radius),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: onTap,
          onLongPress: onLongPress,
          child: ConstrainedBox(
            constraints: const BoxConstraints(minHeight: 40),
            child: Padding(
              padding: EdgeInsets.fromLTRB(8 + indent, 4, 8, 4),
              child: Row(
                children: [
                  SizedBox(
                    width: _icon,
                    child: Center(child: leading),
                  ),
                  const SizedBox(width: _col - _icon),
                  Expanded(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        if (detail == null)
                          Row(
                            children: [
                              Flexible(child: titleText),
                              for (final w in titleSuffix) ...[
                                const SizedBox(width: 6),
                                w,
                              ],
                            ],
                          )
                        else
                          LayoutBuilder(
                            builder: (context, box) {
                              final detailStyle = TextStyle(
                                color: context.tokens.textDim,
                                fontSize: 12,
                                fontFamily: monoFamily,
                              );
                              // The name takes the width it needs and the
                              // branch the rest. When both do not fit, the
                              // name keeps 60% of the line.
                              final detailWidth = _detailWidth(
                                context,
                                detail,
                                detailStyle,
                              );
                              final nameMax = math.max(
                                box.maxWidth * 0.6,
                                box.maxWidth - _detailGap - detailWidth,
                              );
                              return Row(
                                children: [
                                  ConstrainedBox(
                                    constraints: BoxConstraints(
                                      maxWidth: nameMax,
                                    ),
                                    child: titleText,
                                  ),
                                  const SizedBox(width: _detailGap),
                                  Expanded(
                                    child: Text(
                                      detail,
                                      maxLines: 1,
                                      overflow: TextOverflow.ellipsis,
                                      textAlign: TextAlign.right,
                                      style: detailStyle,
                                    ),
                                  ),
                                ],
                              );
                            },
                          ),
                      ],
                    ),
                  ),
                  if (trailing != null) ...[const SizedBox(width: 8), trailing],
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }

  static const _detailGap = 8.0;

  // Branch widths by text, for the text scale in [_widthsScaler]. The drawer
  // rebuilds on every session update; the branches seldom change.
  final _detailWidths = <String, double>{};
  TextScaler? _widthsScaler;

  double _detailWidth(BuildContext context, String detail, TextStyle style) {
    final scaler = MediaQuery.textScalerOf(context);
    if (scaler != _widthsScaler) {
      _detailWidths.clear();
      _widthsScaler = scaler;
    }
    return _detailWidths[detail] ??= measureText(
      context,
      TextSpan(text: detail),
      style,
    ).width;
  }
}
