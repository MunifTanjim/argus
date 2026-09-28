import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/project.dart';
import '../models/workspace_files.dart';
import '../state/navigation.dart';
import '../state/workspace.dart';
import 'file_view_screen.dart';
import 'responsive.dart';
import 'theme.dart';

/// Browses the workspace tree. The path lives in [filesPathProvider], so back
/// in the home shell can walk up one folder.
class WorkspaceFilesTab extends ConsumerWidget {
  const WorkspaceFilesTab({super.key, required this.workspace});

  final WorkspaceNode workspace;

  void _go(WidgetRef ref, String path) =>
      ref.read(filesPathProvider(workspace.id).notifier).state = path;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final path = ref.watch(filesPathProvider(workspace.id));
    final async = ref.watch(workspaceDirProvider((workspace.id, path)));
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        _breadcrumb(ref, path),
        const Divider(height: 1),
        Expanded(
          child: RefreshIndicator(
            onRefresh: () async =>
                ref.invalidate(workspaceDirProvider((workspace.id, path))),
            child: async.when(
              loading: () => const Center(child: CircularProgressIndicator()),
              error: (e, _) => ListView(
                padding: const EdgeInsets.all(24),
                children: [
                  Text(
                    'Could not open this folder:\n$e',
                    style: const TextStyle(color: AppColors.dim),
                  ),
                  const SizedBox(height: 8),
                  Align(
                    alignment: Alignment.centerLeft,
                    child: TextButton(
                      onPressed: () => ref.invalidate(
                        workspaceDirProvider((workspace.id, path)),
                      ),
                      child: const Text('Retry'),
                    ),
                  ),
                ],
              ),
              data: (listing) => CenteredBody(
                child: ListView(
                  children: [
                    for (final e in listing.entries) _entry(context, ref, e),
                  ],
                ),
              ),
            ),
          ),
        ),
      ],
    );
  }

  Widget _breadcrumb(WidgetRef ref, String path) {
    final parts = path.isEmpty ? const <String>[] : path.split('/');
    final crumbs = <Widget>[
      _crumb(workspace.name, () => _go(ref, ''), key: const Key('files-root')),
    ];
    for (var i = 0; i < parts.length; i++) {
      final to = parts.sublist(0, i + 1).join('/');
      crumbs
        ..add(const Text(' / ', style: TextStyle(color: AppColors.dim)))
        ..add(_crumb(parts[i], () => _go(ref, to)));
    }
    return SingleChildScrollView(
      scrollDirection: Axis.horizontal,
      padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 2),
      child: Row(children: crumbs),
    );
  }

  Widget _crumb(String label, VoidCallback onTap, {Key? key}) => InkWell(
    key: key,
    onTap: onTap,
    borderRadius: BorderRadius.circular(4),
    child: Container(
      constraints: const BoxConstraints(minHeight: 40),
      padding: const EdgeInsets.symmetric(horizontal: 6),
      alignment: Alignment.center,
      child: Text(label, style: const TextStyle(color: AppColors.link)),
    ),
  );

  Widget _entry(BuildContext context, WidgetRef ref, DirEntry e) {
    final icon = e.isDir
        ? Icons.folder_outlined
        : (e.symlink ? Icons.link : Icons.description_outlined);
    return ListTile(
      dense: true,
      leading: Icon(icon, size: 18),
      title: Text(e.name, maxLines: 1, overflow: TextOverflow.ellipsis),
      trailing: e.symlink
          ? Text(
              '→ ${e.target}',
              style: const TextStyle(color: AppColors.dim, fontSize: 11),
            )
          : null,
      onTap: () => e.isDir
          ? _go(ref, e.path)
          : Navigator.of(context).push(
              MaterialPageRoute(
                builder: (_) =>
                    FileViewScreen(workspaceId: workspace.id, path: e.path),
              ),
            ),
    );
  }
}
