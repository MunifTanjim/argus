import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:super_sliver_list/super_sliver_list.dart';

import '../models/entry.dart';
import '../state/appearance.dart';
import '../state/tool_detail.dart';
import 'entry_row.dart';
import 'prompt_scrollbar.dart';
import 'responsive.dart';
import 'theme.dart';

/// One row of the feed: a single entry, or a run of tool calls folded behind
/// a toggle.
sealed class FeedRow {
  const FeedRow();
}

class EntryFeedRow extends FeedRow {
  const EntryFeedRow(this.entry);
  final Entry entry;
}

class ToolGroupFeedRow extends FeedRow {
  const ToolGroupFeedRow(this.key, this.tools);

  /// The first tool's id: stable as the run grows, since entries are append-only.
  final String key;
  final List<Entry> tools;
}

bool _isBlankText(Entry e) =>
    e.kind == EntryKind.text && (e.text ?? '').trim().isEmpty;

/// Folds each maximal run of tool calls into one [ToolGroupFeedRow] when
/// [collapseTools] is on. Blank text entries are transparent inside a run;
/// any other entry ends it.
List<FeedRow> groupEntries(List<Entry> entries, {required bool collapseTools}) {
  if (!collapseTools) return [for (final e in entries) EntryFeedRow(e)];
  final rows = <FeedRow>[];
  var i = 0;
  while (i < entries.length) {
    final e = entries[i];
    if (!e.isToolCall) {
      rows.add(EntryFeedRow(e));
      i++;
      continue;
    }
    final tools = <Entry>[];
    while (i < entries.length &&
        (entries[i].isToolCall || _isBlankText(entries[i]))) {
      if (entries[i].isToolCall) tools.add(entries[i]);
      i++;
    }
    rows.add(ToolGroupFeedRow(tools.first.id, tools));
  }
  return rows;
}

/// Used by both the session detail and subagent trace screens.
///
/// When [stickToBottom] is true (live feeds) the view opens pinned to the
/// bottom and tails new items while the user stays at the bottom; once the user
/// scrolls up it stops following until they return to the bottom. Static views
/// (history, inlined traces) pass false to keep the natural top-anchored scroll.
class TranscriptFeed extends ConsumerStatefulWidget {
  const TranscriptFeed({
    super.key,
    required this.detailRef,
    required this.entries,
    this.emptyText = 'No transcript yet.',
    this.stickToBottom = true,
  });

  /// Addresses the transcript so tool rows can fetch their bodies on demand.
  final ToolDetailRef detailRef;
  final List<Entry> entries;
  final String emptyText;
  final bool stickToBottom;

  @override
  ConsumerState<TranscriptFeed> createState() => _TranscriptFeedState();
}

class _TranscriptFeedState extends ConsumerState<TranscriptFeed> {
  final ScrollController _sc = ScrollController();
  final ListController _lc = ListController();

  // Group keys the user has expanded.
  final Set<String> _revealed = {};

  // Ids of entries whose body the user expanded. Kept here, not in the rows,
  // because a row's state is dropped when it scrolls out of the list.
  final Set<String> _expanded = {};

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
        !identical(widget.entries, old.entries)) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _jumpToBottom());
    }
  }

  @override
  void dispose() {
    _sc.dispose();
    _lc.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final controller = widget.stickToBottom ? _sc : null;
    if (widget.entries.isEmpty) {
      // No width cap on the empty state: a single centred line gains nothing
      // from it.
      return ListView(
        controller: controller,
        children: [
          const SizedBox(height: 120),
          Center(
            child: Text(
              widget.emptyText,
              style: const TextStyle(color: AppColors.dim),
            ),
          ),
        ],
      );
    }
    final collapse = ref.watch(
      appearancePrefsProvider.select((p) => p.collapseToolCalls),
    );
    final rows = groupEntries(widget.entries, collapseTools: collapse);
    return CenteredBody(
      child: PromptScrollbar(
        rows: rows,
        listController: _lc,
        scrollController: _sc,
        child: SuperListView.builder(
          controller: _sc,
          listController: _lc,
          padding: const EdgeInsets.all(12),
          itemCount: rows.length,
          itemBuilder: (_, i) => switch (rows[i]) {
            EntryFeedRow(:final entry) => EntryRow(
              key: ValueKey(entry.id),
              detailRef: widget.detailRef,
              entry: entry,
              expanded: _expanded.contains(entry.id),
              onToggle: () => setState(() {
                if (!_expanded.remove(entry.id)) _expanded.add(entry.id);
              }),
            ),
            ToolGroupFeedRow(:final key, :final tools) => _toolGroup(
              key,
              tools,
            ),
          },
        ),
      ),
    );
  }

  Widget _toolGroup(String key, List<Entry> tools) {
    final revealed = _revealed.contains(key);
    final noun = tools.length == 1 ? 'tool call' : 'tool calls';
    final toggle = InkWell(
      borderRadius: BorderRadius.circular(4),
      onTap: () => setState(() {
        if (!_revealed.remove(key)) _revealed.add(key);
      }),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 4),
        child: Text(
          revealed ? '▾ hide $noun' : '▸ ${tools.length} $noun',
          style: const TextStyle(
            fontFamily: 'monospace',
            fontSize: 11,
            color: AppColors.dim,
          ),
        ),
      ),
    );
    return Column(
      key: ValueKey('group:$key'),
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (revealed)
          for (final t in tools)
            EntryRow(
              key: ValueKey(t.id),
              detailRef: widget.detailRef,
              entry: t,
            ),
        toggle,
      ],
    );
  }
}
