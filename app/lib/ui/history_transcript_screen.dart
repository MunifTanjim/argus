import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/history_repository.dart';
import '../models/entry.dart';
import '../models/history.dart';
import '../state/control.dart';
import '../state/tool_detail.dart';
import 'history_details.dart';
import 'history_format.dart';
import 'resume_action.dart';
import 'session_composer.dart' show dockPadding;
import 'theme.dart';
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
    // Resume reopens in the original cwd, which some sessions (e.g.
    // antigravity) don't record. A node older than the app's minimum may not
    // understand the request.
    final canResume = widget.session.resumable &&
        (widget.project?.cwd.isNotEmpty ?? false) &&
        !(widget.project?.missing ?? false) &&
        !ref.watch(outdatedNodeIdsProvider
            .select((ids) => ids.contains(widget.session.nodeId)));
    return Scaffold(
      appBar: AppBar(
        title: HistoryTitle(
          text: widget.session.displayTitle,
          onTap: () => showHistorySessionDetails(
              context, widget.session, widget.project),
        ),
      ),
      // top:false — AppBar insets the top; bottom safe-area keeps the feed
      // clear of the system navigation bar (e.g. Android 3-button nav).
      body: SafeArea(
        top: false,
        child: Column(
          children: [
            Expanded(child: _buildBody()),
            if (canResume)
              _ResumeBar(
                machine: firstLabel([
                  widget.session.nodeLabel,
                  widget.project?.nodeLabel,
                  widget.session.nodeId,
                ]),
                cwd: widget.project!.cwd,
                onTap: _resumeSession,
              ),
          ],
        ),
      ),
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

/// Resume, where the composer sits in a live session, naming where the
/// session will reopen.
class _ResumeBar extends StatelessWidget {
  const _ResumeBar({
    required this.machine,
    required this.cwd,
    required this.onTap,
  });

  final String? machine;
  final String cwd;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final radius = BorderRadius.circular(t.radius + 4);
    final where = [
      if (machine?.isNotEmpty ?? false) machine!,
      shortenPath(cwd),
    ].join(' · ');
    return Material(
      color: t.canvas,
      child: Padding(
        padding: dockPadding,
        child: Semantics(
          button: true,
          label: 'Resume',
          child: InkWell(
            key: const Key('history-resume-bar'),
            onTap: onTap,
            borderRadius: radius,
            child: Container(
              padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
              decoration: BoxDecoration(
                border: Border.all(color: t.line),
                borderRadius: radius,
              ),
              child: Row(
                children: [
                  const Icon(Icons.play_arrow, size: 18),
                  const SizedBox(width: 6),
                  const Text('Resume'),
                  const SizedBox(width: 12),
                  Expanded(
                    child: Row(
                      mainAxisAlignment: MainAxisAlignment.end,
                      children: [
                        if (machine?.isNotEmpty ?? false) ...[
                          Icon(Icons.dns_outlined, size: 13, color: t.textDim),
                          const SizedBox(width: 4),
                        ],
                        Flexible(
                          child: Text(
                            where,
                            maxLines: 1,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(fontSize: 12, color: t.textDim),
                          ),
                        ),
                      ],
                    ),
                  ),
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
