import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/session.dart';
import '../state/changes.dart';
import 'changes_view.dart';

/// A live session's git status (grouped Staged / Unstaged / Untracked) above its
/// branch/unpushed commits.
class ChangedFilesScreen extends ConsumerWidget {
  const ChangedFilesScreen({super.key, required this.session});

  final Session session;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final source = SessionChangesSource(session.id);
    return Scaffold(
      appBar: AppBar(
        title: const Text('Changes'),
        actions: [
          IconButton(
            icon: const Icon(Icons.refresh),
            tooltip: 'Refresh',
            onPressed: () => refreshChanges(ref, source),
          ),
        ],
      ),
      body: SafeArea(top: false, child: ChangesView(source: source)),
    );
  }
}
