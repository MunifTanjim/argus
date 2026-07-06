import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/history_repository.dart';
import '../models/chunk.dart';
import '../models/history.dart';
import '../state/tool_detail.dart';
import 'transcript_feed.dart';

class HistoryTranscriptScreen extends ConsumerStatefulWidget {
  const HistoryTranscriptScreen({super.key, required this.session});

  final HistorySession session;

  @override
  ConsumerState<HistoryTranscriptScreen> createState() =>
      _HistoryTranscriptScreenState();
}

class _HistoryTranscriptScreenState
    extends ConsumerState<HistoryTranscriptScreen> {
  List<Chunk>? _chunks;
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
          _chunks = value;
        case Error(:final error):
          _error = error;
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(widget.session.displayTitle)),
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
    final chunks = _chunks;
    if (chunks == null) {
      return const Center(child: CircularProgressIndicator());
    }
    return TranscriptFeed(
      detailRef: ToolDetailRef.history(
        nodeId: widget.session.nodeId,
        transcriptPath: widget.session.transcriptPath,
        agent: widget.session.agent,
      ),
      chunks: chunks,
      emptyText: 'Empty transcript.',
      stickToBottom: false, // static history reads top-down
    );
  }
}
