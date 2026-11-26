import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/changes.dart';
import '../state/changes.dart';
import '../state/workspace.dart';
import 'changed_file_review_screen.dart';
import 'changed_file_row.dart';
import 'responsive.dart';
import 'theme.dart';
import 'top_bar.dart';

/// Lists the files a commit changed; tap one to review its parent-vs-commit diff.
class CommitDetailScreen extends ConsumerWidget {
  const CommitDetailScreen({
    super.key,
    required this.source,
    required this.commit,
  });

  final WorkspaceChangesSource source;
  final Commit commit;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final t = context.tokens;
    final async = ref.watch(
      workspaceCommitFilesProvider((source.workspaceId, commit.sha)),
    );
    return Scaffold(
      appBar: TopBar(
        title: Text('${commit.short}  ${commit.subject}'),
      ),
      body: SafeArea(
        top: false,
        child: async.when(
          loading: () => const Center(child: CircularProgressIndicator()),
          error: (e, _) => Center(
            child: Text('Could not load commit:\n$e',
                style: TextStyle(color: t.textDim)),
          ),
          data: (files) => files.isEmpty
              ? Center(
                  child: Text('No files in this commit.',
                      style: TextStyle(color: t.textDim)))
              : CenteredBody(
                  child: ListView(
                    padding: const EdgeInsets.all(12),
                    children: [
                      for (final f in files)
                        ChangedFileRow(
                          file: f,
                          onTap: () => Navigator.of(context).push(
                            MaterialPageRoute(
                              builder: (_) => ChangedFileReviewScreen(
                                source: source,
                                file: f,
                                rev: commit.sha,
                              ),
                            ),
                          ),
                        ),
                    ],
                  ),
                ),
        ),
      ),
    );
  }
}
