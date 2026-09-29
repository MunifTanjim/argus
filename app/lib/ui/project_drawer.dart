import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/project.dart';
import '../state/gateway.dart';
import '../state/navigation.dart';
import '../state/project_tree.dart';
import '../state/projects.dart';
import '../state/projects_api.dart';
import '../state/sessions.dart';
import 'project_actions.dart';
import 'settings_screen.dart';
import 'theme.dart';

const _amber = Color(0xFFd79921);
const _green = Color(0xFF689d6a);

class ProjectDrawer extends ConsumerStatefulWidget {
  const ProjectDrawer({super.key});

  @override
  ConsumerState<ProjectDrawer> createState() => _ProjectDrawerState();
}

class _ProjectDrawerState extends ConsumerState<ProjectDrawer> {
  late final _filter = TextEditingController(
    text: ref.read(treeViewProvider).filter,
  );

  @override
  void dispose() {
    _filter.dispose();
    super.dispose();
  }

  Future<void> _refresh() => ref
      .read(projectsProvider.notifier)
      .load(ref.read(gatewayProvider)?.client);

  void _close() => Scaffold.maybeOf(context)?.closeDrawer();

  void _select(String? workspaceId) {
    ref.read(scopeProvider.notifier).state = workspaceId;
    _close();
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

    return Material(
      color: AppColors.card,
      child: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            _header(view),
            _search(),
            Expanded(
              child: RefreshIndicator(
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
                            child: CircularProgressIndicator(strokeWidth: 2),
                          ),
                        ),
                      ),
                    if (projects.error != null && projects.projects.isEmpty)
                      _errorRow(projects.error!),
                    for (final r in rows)
                      _treeRow(r, act, scope, projects.projects, removing),
                  ],
                ),
              ),
            ),
            const Divider(height: 1),
            Padding(
              padding: const EdgeInsets.only(top: 6, bottom: 10),
              child: _row(
                onTap: _openSettings,
                leading: const Icon(Icons.settings_outlined, size: 18),
                title: 'Settings',
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _header(TreeView view) => Padding(
    padding: const EdgeInsets.fromLTRB(16, 8, 4, 4),
    child: Row(
      children: [
        const Expanded(
          child: Text(
            'Argus',
            style: TextStyle(fontWeight: FontWeight.w700, fontSize: 16),
          ),
        ),
        PopupMenuButton<String>(
          tooltip: 'Tree options',
          onSelected: (v) {
            final notifier = ref.read(treeViewProvider.notifier);
            switch (v) {
              case 'hidden':
                notifier.toggleHidden();
              case 'gone':
                notifier.toggleGone();
              default:
                _refresh();
            }
          },
          itemBuilder: (_) => [
            PopupMenuItem(
              value: 'hidden',
              child: _menuToggle('Show hidden', view.showHidden),
            ),
            PopupMenuItem(
              value: 'gone',
              child: _menuToggle('Show gone', view.showGone),
            ),
            const PopupMenuItem(value: 'refresh', child: Text('Refresh')),
          ],
        ),
      ],
    ),
  );

  // The menu item takes the tap; the checkbox only shows the state.
  Widget _menuToggle(String label, bool on) => Row(
    children: [
      Expanded(child: Text(label)),
      const SizedBox(width: 16),
      IgnorePointer(
        child: Checkbox(
          value: on,
          onChanged: (_) {},
          visualDensity: VisualDensity.compact,
          materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
        ),
      ),
    ],
  );

  Widget _search() => Padding(
    padding: const EdgeInsets.fromLTRB(8, 4, 8, 8),
    child: SizedBox(
      height: 36,
      child: TextField(
        controller: _filter,
        style: const TextStyle(fontSize: 13),
        decoration: InputDecoration(
          isDense: true,
          filled: true,
          fillColor: AppColors.canvas,
          prefixIcon: const Icon(Icons.search, size: 16, color: AppColors.dim),
          prefixIconConstraints: const BoxConstraints(minWidth: 40),
          hintText: 'Filter projects',
          hintStyle: const TextStyle(fontSize: 13, color: AppColors.dim),
          contentPadding: const EdgeInsets.symmetric(vertical: 10),
          border: OutlineInputBorder(
            borderRadius: BorderRadius.circular(8),
            borderSide: BorderSide.none,
          ),
        ),
        onChanged: ref.read(treeViewProvider.notifier).setFilter,
      ),
    ),
  );

  Widget _errorRow(String error) => Padding(
    padding: const EdgeInsets.all(12),
    child: Row(
      children: [
        Expanded(
          child: Text(
            'Could not load projects: $error',
            style: const TextStyle(color: AppColors.error, fontSize: 12),
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
        return Padding(
          padding: const EdgeInsets.fromLTRB(8, 10, 8, 0),
          child: InkWell(
            borderRadius: BorderRadius.circular(8),
            onTap: () => ref.read(treeViewProvider.notifier).toggleFold(r.id),
            child: ConstrainedBox(
              constraints: const BoxConstraints(minHeight: 32),
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 8),
                child: Row(
                  children: [
                    Expanded(
                      child: Text(
                        r.label.toUpperCase(),
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: const TextStyle(
                          color: AppColors.dim,
                          fontSize: 11,
                          fontWeight: FontWeight.w600,
                          letterSpacing: 0.8,
                        ),
                      ),
                    ),
                    if (r.folded) ?_badges(sumActivity(act, r.workspaceIds)),
                    const SizedBox(width: 4),
                    Icon(
                      r.folded ? Icons.expand_more : Icons.expand_less,
                      size: 18,
                      color: AppColors.dim,
                      semanticLabel: r.folded ? 'expand' : 'collapse',
                    ),
                  ],
                ),
              ),
            ),
          ),
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
                  color: AppColors.dim,
                )
              : null,
          title: _title(r),
          titleStyle: TextStyle(
            fontWeight: FontWeight.w600,
            color: r.hidden ? AppColors.dim : null,
          ),
          titleSuffix: [
            if (r.hidden)
              const Icon(
                Icons.visibility_off_outlined,
                size: 13,
                color: AppColors.dim,
                semanticLabel: 'hidden',
              ),
            if (r.pinned)
              const Icon(Icons.push_pin, size: 13, color: AppColors.dim),
          ],
          trailing: _trailing([
            if (r.gitError.isNotEmpty)
              InkWell(
                borderRadius: BorderRadius.circular(12),
                onTap: () => _showError(r.label, r.gitError),
                child: const Padding(
                  padding: EdgeInsets.all(4),
                  child: Icon(
                    Icons.warning_amber,
                    size: 16,
                    color: AppColors.error,
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
          leading: Icon(
            r.isMain ? Icons.folder_outlined : Icons.call_split,
            size: 16,
            color: AppColors.dim,
            semanticLabel: r.isMain ? 'main worktree' : null,
          ),
          title: _title(r),
          subtitle: r.detail,
          trailing: _trailing([
            if (removing.contains(r.id))
              const Text(
                'removing…',
                style: TextStyle(color: AppColors.dim, fontSize: 12),
              ),
            if (r.setup == SetupMark.running)
              const Icon(
                Icons.sync,
                size: 14,
                color: _amber,
                semanticLabel: 'setup running',
              ),
            if (r.setup == SetupMark.failed)
              const Icon(
                Icons.error_outline,
                size: 14,
                color: AppColors.error,
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

  Widget? _badges(Activity a) =>
      a.live > 0 || a.waiting > 0 ? _Badges(a) : null;

  // The leading icon column plus its gap. A workspace is indented by one
  // column, so its icon sits under its project's name.
  static const _col = 32.0;

  Widget _row({
    required String title,
    required VoidCallback onTap,
    VoidCallback? onLongPress,
    Widget? leading,
    TextStyle? titleStyle,
    List<Widget> titleSuffix = const [],
    String? subtitle,
    Widget? trailing,
    bool selected = false,
    double indent = 0,
  }) => Padding(
    padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 1),
    child: Material(
      color: selected ? AppColors.border : Colors.transparent,
      borderRadius: BorderRadius.circular(8),
      clipBehavior: Clip.antiAlias,
      child: InkWell(
        onTap: onTap,
        onLongPress: onLongPress,
        child: ConstrainedBox(
          constraints: BoxConstraints(minHeight: subtitle == null ? 40 : 52),
          child: Padding(
            padding: EdgeInsets.fromLTRB(8 + indent, 4, 8, 4),
            child: Row(
              children: [
                SizedBox(width: 24, child: Center(child: leading)),
                const SizedBox(width: _col - 24),
                Expanded(
                  child: Column(
                    mainAxisSize: MainAxisSize.min,
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
                      Row(
                        children: [
                          Flexible(
                            child: Text(
                              title,
                              maxLines: 1,
                              overflow: TextOverflow.ellipsis,
                              style: const TextStyle(
                                fontSize: 14,
                              ).merge(titleStyle),
                            ),
                          ),
                          for (final w in titleSuffix) ...[
                            const SizedBox(width: 6),
                            w,
                          ],
                        ],
                      ),
                      if (subtitle != null) ...[
                        const SizedBox(height: 2),
                        Text(
                          subtitle,
                          maxLines: 1,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            color: AppColors.link,
                            fontSize: 12,
                          ),
                        ),
                      ],
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

class _Badges extends StatelessWidget {
  const _Badges(this.activity);
  final Activity activity;

  @override
  Widget build(BuildContext context) => Row(
    mainAxisSize: MainAxisSize.min,
    children: [
      if (activity.waiting > 0) _badge('${activity.waiting}', _amber),
      if (activity.live > 0) _badge('${activity.live}', _green),
    ],
  );

  Widget _badge(String text, Color color) => Container(
    margin: const EdgeInsets.only(left: 4),
    padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 1),
    decoration: BoxDecoration(
      color: color,
      borderRadius: BorderRadius.circular(8),
    ),
    child: Text(
      text,
      style: const TextStyle(
        color: AppColors.canvas,
        fontSize: 10,
        fontWeight: FontWeight.w700,
      ),
    ),
  );
}
