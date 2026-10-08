import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/history_repository.dart';
import '../models/entry.dart';
import '../models/history.dart';
import '../state/tool_detail.dart';
import 'resume_action.dart';
import 'shell_drawer.dart';
import 'transcript_feed.dart';

class HistoryTranscriptScreen extends ConsumerStatefulWidget {
  const HistoryTranscriptScreen({
    super.key,
    required this.session,
    this.project,
  });

  final HistorySession session;
  final HistoryProject? project;

  @override
  ConsumerState<HistoryTranscriptScreen> createState() =>
      _HistoryTranscriptScreenState();
}

class _HistoryTranscriptScreenState
    extends ConsumerState<HistoryTranscriptScreen> {
  List<Entry>? _entries;
  Object? _error;

  @override
  void initState() {
    super.initState();
    _load();
  }

  Future<void> _load() async {
    final result = await ref.read(historyRepositoryProvider).transcript(
          nodeId: widget.session.nodeId,
          transcriptPath: widget.session.transcriptPath,
          agent: widget.session.agent,
        );
    if (!mounted) return;
    setState(() {
      switch (result) {
        case Ok(:final value):
          _entries = value;
        case Error(:final error):
          _error = error;
      }
    });
  }

  Future<void> _resumeSession() async {
    final project = widget.project;
    if (project == null) return;
    final s = widget.session;
    await resumeSession(
      context,
      ref,
      nodeId: s.nodeId,
      agent: s.agent,
      agentSessionId: s.sessionId,
      cwd: project.cwd,
    );
  }

  @override
  Widget build(BuildContext context) {
    // A resume needs the original working directory; some sessions (e.g.
    // antigravity) have an unknown cwd and can't be reopened in place.
    final canResume = widget.session.resumable &&
        (widget.project?.cwd.isNotEmpty ?? false);
    return Scaffold(
      appBar: AppBar(
        title: Text(widget.session.displayTitle),
        actions: [
          ?shellMenuButton(context),
          if (canResume)
            IconButton(
              icon: const Icon(Icons.play_arrow),
              onPressed: _resumeSession,
              tooltip: 'Resume',
            ),
        ],
      ),
      // top:false — AppBar insets the top; bottom safe-area keeps the feed
      // clear of the system navigation bar (e.g. Android 3-button nav).
      body: SafeArea(top: false, child: _buildBody()),
    );
  }

  Widget _buildBody() {
    final error = _error;
    if (error != null) {
      return Center(child: Text(error.toString()));
    }
    final entries = _entries;
    if (entries == null) {
      return const Center(child: CircularProgressIndicator());
    }
    return TranscriptFeed(
      detailRef: ToolDetailRef.history(
        nodeId: widget.session.nodeId,
        transcriptPath: widget.session.transcriptPath,
        agent: widget.session.agent,
      ),
      entries: entries,
      emptyText: 'Empty transcript.',
      stickToBottom: false, // static history reads top-down
    );
  }
}
