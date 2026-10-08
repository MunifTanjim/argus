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
import 'tool_registry.dart';

/// An expanded run is flattened into its own rows (header, entries, footer) so
/// the list stays virtualized.
sealed class FeedRow {
  const FeedRow();
}

class EntryFeedRow extends FeedRow {
  const EntryFeedRow(this.entry);
  final Entry entry;
}

/// A collapsed run.
class RunSummaryFeedRow extends FeedRow {
  const RunSummaryFeedRow(this.key, this.thinking, this.tools);

  /// The run's first entry id: stable as the run grows, since entries are
  /// append-only.
  final String key;
  final int thinking;
  final int tools;
}

/// The header ([top]) or footer control row of an expanded run.
class RunEdgeFeedRow extends FeedRow {
  const RunEdgeFeedRow(
    this.key,
    this.thinking,
    this.tools, {
    required this.top,
  });
  final String key;
  final int thinking;
  final int tools;
  final bool top;
}

/// A subagent spawn (not an agent-ref op like wait_agent) stands alone so it
/// stays visible and drillable.
bool _isSpawn(Entry e) =>
    e.kind == EntryKind.subagent &&
    !e.isTeammate &&
    !isAgentRefTool(e.toolName);

bool _inRun(Entry e) =>
    e.kind == EntryKind.thinking || (e.isToolCall && !_isSpawn(e));

/// Folds each maximal run of thinking/tool entries.
List<FeedRow> groupEntries(
  List<Entry> entries, {
  required bool verbose,
  Map<String, bool> overrides = const {},
}) {
  final rows = <FeedRow>[];
  var i = 0;
  while (i < entries.length) {
    final e = entries[i];
    if (!_inRun(e)) {
      rows.add(EntryFeedRow(e));
      i++;
      continue;
    }
    final run = <Entry>[];
    while (i < entries.length && _inRun(entries[i])) {
      run.add(entries[i++]);
    }
    final key = run.first.id;
    final thinking = run.where((e) => e.kind == EntryKind.thinking).length;
    final tools = run.length - thinking;
    if (overrides[key] ?? verbose) {
      rows.add(RunEdgeFeedRow(key, thinking, tools, top: true));
      for (final e in run) {
        rows.add(EntryFeedRow(e));
      }
      rows.add(RunEdgeFeedRow(key, thinking, tools, top: false));
    } else {
      rows.add(RunSummaryFeedRow(key, thinking, tools));
    }
  }
  return rows;
}

/// "2 thinking · 5 tools", omitting a zero part.
String _runCounts(int thinking, int tools) => [
  if (thinking > 0) '$thinking thinking',
  if (tools > 0) '$tools ${tools == 1 ? 'tool' : 'tools'}',
].join(' · ');

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

  // Per-run expansion the user chose, overriding the verbose default.
  final Map<String, bool> _overrides = {};

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
    final verbose = ref.watch(
      appearancePrefsProvider.select((p) => p.verboseTranscript),
    );
    final rows = groupEntries(
      widget.entries,
      verbose: verbose,
      overrides: _overrides,
    );
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
            RunSummaryFeedRow(:final key, :final thinking, :final tools) =>
              _runToggle(
                ValueKey('run:$key'),
                key,
                true,
                '▸ ${_runCounts(thinking, tools)}',
              ),
            RunEdgeFeedRow(
              :final key,
              :final thinking,
              :final tools,
              :final top,
            ) =>
              top
                  ? _runToggle(
                      ValueKey('run-top:$key'),
                      key,
                      false,
                      '▾ collapse · ${_runCounts(thinking, tools)}',
                    )
                  : _runToggle(
                      ValueKey('run-bottom:$key'),
                      key,
                      false,
                      '▴ collapse',
                    ),
          },
        ),
      ),
    );
  }

  Widget _runToggle(Key key, String runKey, bool expand, String label) {
    return Align(
      key: key,
      alignment: Alignment.centerLeft,
      child: InkWell(
        borderRadius: BorderRadius.circular(4),
        onTap: () => setState(() => _overrides[runKey] = expand),
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: Text(
            label,
            style: const TextStyle(
              fontFamily: 'monospace',
              fontSize: 11,
              color: AppColors.dim,
            ),
          ),
        ),
      ),
    );
  }
}
