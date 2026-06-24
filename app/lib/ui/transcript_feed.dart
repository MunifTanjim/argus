import 'package:flutter/material.dart';

import '../models/chunk.dart';
import '../state/tool_detail.dart';
import 'chunk_card.dart';
import 'theme.dart';

/// The shared transcript body: a feed of [ChunkCard]s with an empty state.
/// Used by both the session detail and subagent trace screens.
///
/// When [stickToBottom] is true (live feeds) the view opens pinned to the
/// bottom and tails new items while the user stays at the bottom; once the user
/// scrolls up it stops following until they return to the bottom. Static views
/// (history, inlined traces) pass false to keep the natural top-anchored scroll.
class TranscriptFeed extends StatefulWidget {
  const TranscriptFeed({
    super.key,
    required this.detailRef,
    required this.chunks,
    this.emptyText = 'No transcript yet.',
    this.stickToBottom = true,
  });

  /// Addresses the transcript so tool rows can fetch their bodies on demand.
  final ToolDetailRef detailRef;
  final List<Chunk> chunks;
  final String emptyText;
  final bool stickToBottom;

  @override
  State<TranscriptFeed> createState() => _TranscriptFeedState();
}

class _TranscriptFeedState extends State<TranscriptFeed> {
  final ScrollController _sc = ScrollController();

  // Whether the view is currently tailing the bottom. Starts true so a freshly
  // opened feed lands on the newest content.
  bool _following = true;

  // Treat "within this many pixels of the bottom" as still following, so a small
  // overscroll/settle doesn't disable tailing.
  static const double _bottomSlack = 24;

  @override
  void initState() {
    super.initState();
    if (widget.stickToBottom) {
      _sc.addListener(_onScroll);
      WidgetsBinding.instance.addPostFrameCallback((_) => _jumpToBottom());
    }
  }

  void _onScroll() {
    if (!_sc.hasClients) return;
    final p = _sc.position;
    _following = p.pixels >= p.maxScrollExtent - _bottomSlack;
  }

  void _jumpToBottom() {
    if (!_sc.hasClients) return;
    _sc.jumpTo(_sc.position.maxScrollExtent);
  }

  @override
  void didUpdateWidget(TranscriptFeed old) {
    super.didUpdateWidget(old);
    // New transcript data arrived (each delta yields a new list). Tail to the
    // bottom only if the user was already there.
    if (widget.stickToBottom &&
        _following &&
        !identical(widget.chunks, old.chunks)) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _jumpToBottom());
    }
  }

  @override
  void dispose() {
    _sc.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final controller = widget.stickToBottom ? _sc : null;
    if (widget.chunks.isEmpty) {
      return ListView(controller: controller, children: [
        const SizedBox(height: 120),
        Center(
            child: Text(widget.emptyText,
                style: const TextStyle(color: AppColors.dim))),
      ]);
    }
    return ListView.builder(
      controller: controller,
      padding: const EdgeInsets.all(12),
      itemCount: widget.chunks.length,
      itemBuilder: (_, i) =>
          ChunkCard(detailRef: widget.detailRef, chunk: widget.chunks[i]),
    );
  }
}
