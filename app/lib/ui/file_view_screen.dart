import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/navigation.dart';
import '../state/workspace.dart';
import 'code_block.dart';
import 'shell_drawer.dart';
import 'theme.dart';

/// One workspace file, highlighted by its name. Pushed over the workspace tabs.
class FileViewScreen extends ConsumerWidget {
  const FileViewScreen({
    super.key,
    required this.workspaceId,
    required this.path,
  });

  final String workspaceId;
  final String path;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(workspaceFileProvider((workspaceId, path)));
    final dir = parentPath(path);
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(path.split('/').last),
            if (dir.isNotEmpty)
              Text(
                dir,
                style: const TextStyle(color: AppColors.dim, fontSize: 12),
              ),
          ],
        ),
        actions: [?shellMenuButton(context)],
      ),
      body: SafeArea(
        top: false,
        child: async.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (e, _) => Center(
            child: Text(
              'Could not read the file:\n$e',
              style: const TextStyle(color: AppColors.dim),
            ),
          ),
          data: (f) => f.notShown
              ? const Center(
                  child: Text(
                    'Binary or too large to show.',
                    style: TextStyle(color: AppColors.dim),
                  ),
                )
              : SingleChildScrollView(
                  padding: const EdgeInsets.all(12),
                  child: codeView(
                    f.content,
                    lang: langFromPath(path),
                    lineNumbers: true,
                    prettyJson: false,
                  ),
                ),
        ),
      ),
    );
  }
}
