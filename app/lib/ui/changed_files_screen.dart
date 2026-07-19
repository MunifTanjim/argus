import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/changes.dart';
import '../models/session.dart';
import '../state/changes.dart';
import 'changed_file_review_screen.dart';
import 'changed_file_row.dart';
import 'code_block.dart';
import 'commit_detail_screen.dart';
import 'relative_time.dart';
import 'responsive.dart';
import 'theme.dart';

const _mono = TextStyle(fontFamily: 'monospace', fontSize: 13);

/// A live session's git status (grouped Staged / Unstaged / Untracked) above its
/// branch/unpushed commits.
class ChangedFilesScreen extends ConsumerWidget {
  const ChangedFilesScreen({super.key, required this.session});

  final Session session;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final async = ref.watch(changedFilesProvider(session.id));
    return Scaffold(
      appBar: AppBar(
        title: const Text('Changes'),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: 'Refresh',
            onPressed: () {
              ref.invalidate(changedFilesProvider(session.id));
              ref.invalidate(commitsProvider(session.id));
            },
          ),
        ],
      ),
      body: SafeArea(
        top: false,
        child: RefreshIndicator(
          onRefresh: () async {
            ref.invalidate(changedFilesProvider(session.id));
            ref.invalidate(commitsProvider(session.id));
          },
          child: async.when(
            loading: () => const Center(child: CircularProgressIndicator()),
            error: (e, _) => _messageList('Could not load changes:\n$e'),
            data: (files) => _body(context, ref, files),
          ),
        ),
      ),
    );
  }

  Widget _messageList(String text) => ListView(
        children: [
          const SizedBox(height: 120),
          Center(child: Text(text, style: const TextStyle(color: AppColors.dim))),
        ],
      );

  Widget _body(BuildContext context, WidgetRef ref, List<ChangedFile> files) {
    final untracked = files.where((f) => f.change == 'untracked').toList();
    final staged =
        files.where((f) => f.change != 'untracked' && f.staged).toList();
    final unstaged =
        files.where((f) => f.change != 'untracked' && !f.staged).toList();

    return CenteredBody(
      child: ListView(
        padding: const EdgeInsets.all(12),
        children: [
          ..._fileSection(context, 'Staged', staged),
          ..._fileSection(context, 'Unstaged', unstaged),
          ..._fileSection(context, 'Untracked', untracked),
          if (files.isEmpty)
            const Padding(
              padding: EdgeInsets.symmetric(vertical: 8),
              child: Text('No working-tree changes.',
                  style: TextStyle(color: AppColors.dim)),
            ),
          ..._commitsSection(context, ref),
        ],
      ),
    );
  }

  List<Widget> _fileSection(
      BuildContext context, String title, List<ChangedFile> files) {
    if (files.isEmpty) return const [];
    return [
      _sectionHeader('${title.toUpperCase()} (${files.length})'),
      for (final f in files)
        ChangedFileRow(
          file: f,
          onTap: () => Navigator.of(context).push(
            MaterialPageRoute(
              builder: (_) => ChangedFileReviewScreen(
                sessionId: session.id,
                file: f,
              ),
            ),
          ),
        ),
      const SizedBox(height: 8),
    ];
  }

  List<Widget> _commitsSection(BuildContext context, WidgetRef ref) {
    final async = ref.watch(commitsProvider(session.id));
    return async.when(
      loading: () => [
        _sectionHeader('COMMITS'),
        const Padding(
          padding: EdgeInsets.symmetric(vertical: 8),
          child: Center(child: CircularProgressIndicator()),
        ),
      ],
      error: (e, _) => [
        _sectionHeader('COMMITS'),
        Padding(
          padding: const EdgeInsets.symmetric(vertical: 8),
          child: Text('Could not load commits: $e',
              style: const TextStyle(color: AppColors.dim)),
        ),
      ],
      data: (list) {
        if (list.commits.isEmpty) return const [];
        return [
          _sectionHeader(
              '${list.unpushed ? 'UNPUSHED' : 'COMMITS'} (${list.commits.length})'),
          for (final c in list.commits) _CommitRow(session: session, commit: c),
          const SizedBox(height: 8),
        ];
      },
    );
  }

  Widget _sectionHeader(String text) => Padding(
        padding: const EdgeInsets.only(bottom: 8, top: 4),
        child: Text(
          '▌ $text',
          style: const TextStyle(
            fontFamily: 'monospace',
            fontSize: 12,
            fontWeight: FontWeight.w700,
            color: AppColors.dim,
          ),
        ),
      );
}

class _CommitRow extends StatelessWidget {
  const _CommitRow({required this.session, required this.commit});
  final Session session;
  final Commit commit;

  @override
  Widget build(BuildContext context) {
    final rel = commit.unixSec == 0
        ? ''
        : relativeTimeFrom(
            DateTime.fromMillisecondsSinceEpoch(commit.unixSec * 1000),
          );
    final meta = rel.isEmpty ? commit.author : '${commit.author} · $rel';
    return InkWell(
      onTap: () => Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) =>
              CommitDetailScreen(sessionId: session.id, commit: commit),
        ),
      ),
      child: Container(
        margin: const EdgeInsets.only(bottom: 6),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 10),
        decoration: BoxDecoration(
          color: AppColors.card,
          border: Border.all(color: AppColors.border),
          borderRadius: BorderRadius.circular(4),
        ),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(commit.subject,
                style: _mono.copyWith(color: AppColors.text),
                maxLines: 2,
                overflow: TextOverflow.ellipsis),
            const SizedBox(height: 6),
            Row(
              crossAxisAlignment: CrossAxisAlignment.end,
              children: [
                Expanded(
                  child: Text(meta,
                      style: _mono.copyWith(
                          color: AppColors.dim, fontSize: 11)),
                ),
                const SizedBox(width: 8),
                // Inner InkWell absorbs the tap, so copying the SHA doesn't
                // trigger the row's navigation.
                InkWell(
                  onTap: () => copyToClipboard(context, commit.sha),
                  borderRadius: BorderRadius.circular(4),
                  child: Padding(
                    padding:
                        const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
                    child: Row(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Text(commit.short,
                            style: _mono.copyWith(
                                color: const Color(0xFFd79921),
                                fontWeight: FontWeight.w700,
                                fontSize: 11)),
                        const SizedBox(width: 4),
                        const Icon(Icons.copy,
                            size: 12, color: AppColors.dim),
                      ],
                    ),
                  ),
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
