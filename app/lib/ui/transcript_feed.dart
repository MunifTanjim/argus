import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:super_sliver_list/super_sliver_list.dart';

import '../models/entry.dart';
import '../state/appearance.dart';
import '../state/tool_detail.dart';
import 'entry_row.dart';
import 'prompt_scrollbar.dart';
import 'responsive.dart';
import 'run_summary.dart';
import 'theme.dart';
import 'tool_registry.dart';

/// An expanded run is flattened into its own rows (header, entries, footer) so
/// the list stays virtualized.
sealed class FeedRow {
  const FeedRow();
}

class EntryFeedRow extends FeedRow {
  const EntryFeedRow(this.entry, {this.showModel = true});
  final Entry entry;

  /// False for a turn footer whose model is the previous footer's; it hides
  /// the model, so a model switch stands out.
  final bool showModel;
}

/// Run summaries kept across rebuilds. Each delta makes new rows, but a run
/// whose entries are the same objects keeps its summary.
class RunSummaryCache {
  RunSummaryCache({this.agent});
  final String? agent;
  final _byKey = <String, (List<Entry>, RunSummary)>{};

  RunSummary of(String key, List<Entry> entries) {
    final hit = _byKey[key];
    if (hit != null && _same(hit.$1, entries)) return hit.$2;
    final summary = summarizeRun(entries, agent: agent);
    _byKey[key] = (entries, summary);
    return summary;
  }

  static bool _same(List<Entry> a, List<Entry> b) {
    if (a.length != b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (!identical(a[i], b[i])) return false;
    }
    return true;
  }
}

/// A collapsed run.
class RunSummaryFeedRow extends FeedRow {
  RunSummaryFeedRow(this.key, this.entries, this._summaries);

  /// The run's first entry id: stable as the run grows, since entries are
  /// append-only.
  final String key;

  /// The entries the summary counts; a live run's newest tool is left out.
  final List<Entry> entries;
  final RunSummaryCache _summaries;

  /// Made on the first build of the row.
  late final RunSummary summary = _summaries.of(key, entries);
}

/// The header ([top]) or footer control row of an expanded run.
class RunEdgeFeedRow extends FeedRow {
  RunEdgeFeedRow(this.key, this.entries, this._summaries, {required this.top});

  final String key;
  final List<Entry> entries;
  final bool top;
  final RunSummaryCache _summaries;

  /// Made on the first build of the row.
  late final RunSummary summary = _summaries.of(key, entries);
}

/// A subagent spawn (not an agent-ref op like wait_agent) stands alone so it
/// stays visible and drillable.
bool _isSpawn(Entry e) =>
    e.kind == EntryKind.subagent &&
    !e.isTeammate &&
    !isAgentRefTool(e.toolName);

bool _inRun(Entry e) =>
    e.kind == EntryKind.thinking || (e.isToolCall && !_isSpawn(e));

/// Folds each maximal run of thinking/tool entries. When [live] (the session
/// is working), the newest run's last tool stays out of its fold, so what runs
/// now is in view. [summaries] keeps run summaries across calls.
List<FeedRow> groupEntries(
  List<Entry> entries, {
  required bool verbose,
  Map<String, bool> overrides = const {},
  bool live = false,
  RunSummaryCache? summaries,
}) {
  final cache = summaries ?? RunSummaryCache();
  final rows = <FeedRow>[];
  String? lastModel;
  var i = 0;
  while (i < entries.length) {
    final e = entries[i];
    if (!_inRun(e)) {
      var showModel = true;
      if (e.kind == EntryKind.turnEnd) {
        final model = e.modelName;
        if (model != null && model.isNotEmpty) {
          showModel = model != lastModel;
          lastModel = model;
        }
      }
      rows.add(EntryFeedRow(e, showModel: showModel));
      i++;
      continue;
    }
    final run = <Entry>[];
    while (i < entries.length && _inRun(entries[i])) {
      run.add(entries[i++]);
    }
    final key = run.first.id;
    if (overrides[key] ?? verbose) {
      rows.add(RunEdgeFeedRow(key, run, cache, top: true));
      for (final e in run) {
        rows.add(EntryFeedRow(e));
      }
      rows.add(RunEdgeFeedRow(key, run, cache, top: false));
    } else if (live && i == entries.length && run.last.isToolCall) {
      final folded = run.sublist(0, run.length - 1);
      if (folded.isNotEmpty) {
        rows.add(RunSummaryFeedRow(key, folded, cache));
      }
      rows.add(EntryFeedRow(run.last));
    } else {
      rows.add(RunSummaryFeedRow(key, run, cache));
    }
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
    this.header,
    this.onHeaderVisible,
    this.bottomInset = 0,
    this.live = false,
  });

  /// Addresses the transcript so tool rows can fetch their bodies on demand.
  final ToolDetailRef detailRef;
  final List<Entry> entries;
  final String emptyText;
  final bool stickToBottom;

  /// Shown above the first row and scrolled with the rows.
  final Widget? header;

  /// Called when [header] scrolls out of view (false) or back (true).
  final ValueChanged<bool>? onHeaderVisible;

  /// Room below the last row for what floats over the feed's bottom edge.
  final double bottomInset;

  /// The session is working: the newest tool stays out of its fold.
  final bool live;

  @override
  ConsumerState<TranscriptFeed> createState() => TranscriptFeedState();
}

/// Keeps the content at the bottom edge in place when the viewport resizes, so
/// the soft keyboard or a growing composer pushes the transcript up instead of
/// covering it. Only while [anchored] holds, so static feeds stay top-anchored.
class _BottomAnchoredScrollController extends ScrollController {
  _BottomAnchoredScrollController(this.anchored);

  final bool Function() anchored;

  @override
  ScrollPosition createScrollPosition(
    ScrollPhysics physics,
    ScrollContext context,
    ScrollPosition? oldPosition,
  ) => _BottomAnchoredScrollPosition(
    anchored: anchored,
    physics: physics,
    context: context,
    oldPosition: oldPosition,
  );
}

class _BottomAnchoredScrollPosition extends ScrollPositionWithSingleContext {
  _BottomAnchoredScrollPosition({
    required this.anchored,
    required super.physics,
    required super.context,
    super.oldPosition,
  });

  final bool Function() anchored;

  @override
  bool applyViewportDimension(double viewportDimension) {
    final old = hasViewportDimension ? this.viewportDimension : null;
    final changed = super.applyViewportDimension(viewportDimension);
    if (old != null && anchored() && hasPixels && hasContentDimensions) {
      final to = math.max(minScrollExtent, pixels + old - viewportDimension);
      if (to != pixels) correctPixels(to);
    }
    return changed;
  }
}

class TranscriptFeedState extends ConsumerState<TranscriptFeed> {
  late final ScrollController _sc = _BottomAnchoredScrollController(
    () => widget.stickToBottom,
  );
  final ListController _lc = ListController();

  // Per-run expansion the user chose, overriding the verbose default.
  final Map<String, bool> _overrides = {};

  // Ids of entries whose body the user expanded. Kept here, not in the rows,
  // because a row's state is dropped when it scrolls out of the list.
  final Set<String> _expanded = {};

  // The rows last built, for finding a run's edges after it expands. Rebuilt
  // only when the entries, verbose, or overrides change.
  List<FeedRow> _rows = const [];
  List<Entry>? _rowsEntries;
  bool? _rowsVerbose;
  bool? _rowsLive;
  RunSummaryCache? _summaries;

  // Whether the view is currently tailing the bottom. Starts true so a freshly
  // opened feed lands on the newest content.
  bool _following = true;

  // Treat "within this many pixels of the bottom" as still following, so a small
  // overscroll/settle doesn't disable tailing.
  static const double _bottomSlack = 24;

  static const double _pad = 12;

  final _headerKey = GlobalKey();
  bool? _headerShown;

  /// Checks the header after the next frame, when the rows under it change.
  void _syncHeaderLater() {
    if (widget.header == null) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (mounted) _syncHeader();
    });
  }

  @override
  void initState() {
    super.initState();
    _syncHeaderLater();
    _sc.addListener(_syncHeader);
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

  // Reports when the scroll offset passes the header's bottom edge.
  void _syncHeader() {
    final report = widget.onHeaderVisible;
    if (report == null || widget.header == null) return;
    final bool shown;
    if (widget.entries.isEmpty) {
      // The empty state shows the header at the top, unscrolled.
      shown = true;
    } else {
      final box = _headerKey.currentContext?.findRenderObject() as RenderBox?;
      if (!_sc.hasClients || box == null || !box.hasSize) return;
      shown = _sc.position.pixels < box.size.height + _pad;
    }
    if (shown == _headerShown) return;
    _headerShown = shown;
    report(shown);
  }

  /// Scrolls down until the expanded run's bottom row shows, then back to its
  /// top row if the run is taller than the screen.
  void _revealRun(String key) {
    int edge(bool top) => _rows.indexWhere(
      (r) => r is RunEdgeFeedRow && r.key == key && r.top == top,
    );
    void jump(int index, double alignment) {
      _lc.jumpToItem(index: index, scrollController: _sc, alignment: alignment);
      // Aligning a trailing row past the end overshoots; pin to the end.
      final p = _sc.position;
      if (p.pixels > p.maxScrollExtent) _sc.jumpTo(p.maxScrollExtent);
    }

    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !_lc.isAttached || !_sc.hasClients) return;
      final foot = edge(false), range = _lc.visibleRange;
      if (foot < 0 || range == null || foot < range.$2) return;
      jump(foot, 1);
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!mounted || !_lc.isAttached) return;
        final head = edge(true), after = _lc.visibleRange;
        if (head >= 0 && after != null && head < after.$1) jump(head, 0);
      });
    });
  }

  /// Resumes tailing from the bottom, as after the user sends a prompt.
  void follow() {
    if (!widget.stickToBottom || !_sc.hasClients) return;
    _following = true;
    WidgetsBinding.instance.addPostFrameCallback((_) => _jumpToBottom());
  }

  void _jumpToBottom() {
    if (!_sc.hasClients) return;
    _sc.jumpTo(_sc.position.maxScrollExtent);
    _syncHeader();
  }

  @override
  void didUpdateWidget(TranscriptFeed oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (!identical(widget.entries, oldWidget.entries) ||
        widget.header != oldWidget.header ||
        widget.bottomInset != oldWidget.bottomInset) {
      _syncHeaderLater();
    }
    // New transcript data arrived (each delta yields a new list), the room
    // below the last row changed, or the live row came or went. Tail to the
    // bottom only if the user was already there.
    if (widget.stickToBottom &&
        _following &&
        (!identical(widget.entries, oldWidget.entries) ||
            widget.bottomInset != oldWidget.bottomInset ||
            widget.live != oldWidget.live)) {
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
    final t = context.tokens;
    final controller = widget.stickToBottom ? _sc : null;
    final header = widget.header;
    if (widget.entries.isEmpty) {
      // No width cap on the empty state: a single centred line gains nothing
      // from it.
      return ListView(
        controller: controller,
        children: [
          if (header != null)
            Padding(
              padding: const EdgeInsets.fromLTRB(_pad, _pad, _pad, 0),
              child: header,
            ),
          const SizedBox(height: 120),
          Center(
            child: Text(widget.emptyText, style: TextStyle(color: t.textDim)),
          ),
        ],
      );
    }
    final verbose = ref.watch(
      appearancePrefsProvider.select((p) => p.verboseTranscript),
    );
    if (!identical(_rowsEntries, widget.entries) ||
        _rowsVerbose != verbose ||
        _rowsLive != widget.live) {
      _rowsEntries = widget.entries;
      _rowsVerbose = verbose;
      _rowsLive = widget.live;
      final agent = widget.detailRef.agent;
      if (_summaries?.agent != agent) {
        _summaries = RunSummaryCache(agent: agent);
      }
      _rows = groupEntries(
        widget.entries,
        verbose: verbose,
        overrides: _overrides,
        live: widget.live,
        summaries: _summaries,
      );
    }
    final rows = _rows;
    return CenteredBody(
      child: PromptScrollbar(
        rows: rows,
        listController: _lc,
        scrollController: _sc,
        child: CustomScrollView(
          controller: _sc,
          slivers: [
            if (header != null)
              SliverPadding(
                padding: const EdgeInsets.fromLTRB(_pad, _pad, _pad, 0),
                sliver: SliverToBoxAdapter(
                  child: KeyedSubtree(key: _headerKey, child: header),
                ),
              ),
            SliverPadding(
              padding: EdgeInsets.fromLTRB(
                _pad,
                _pad,
                _pad,
                _pad + widget.bottomInset,
              ),
              sliver: SuperSliverList.builder(
                listController: _lc,
                // As SuperListView does: build the cache area at once, so extents settle.
                delayPopulatingCacheArea: false,
                itemCount: rows.length,
                itemBuilder: (_, i) => switch (rows[i]) {
                  EntryFeedRow(:final entry, :final showModel) => EntryRow(
                    key: ValueKey(entry.id),
                    detailRef: widget.detailRef,
                    entry: entry,
                    showModel: showModel,
                    expanded: _expanded.contains(entry.id),
                    onToggle: () => setState(() {
                      if (!_expanded.remove(entry.id)) _expanded.add(entry.id);
                    }),
                  ),
                  RunSummaryFeedRow(:final key, :final summary) => _runToggle(
                    ValueKey('run:$key'),
                    key,
                    true,
                    Icons.chevron_right,
                    summary,
                  ),
                  RunEdgeFeedRow(:final key, :final summary, :final top) =>
                    _runToggle(
                      ValueKey('${top ? 'run-top' : 'run-bottom'}:$key'),
                      key,
                      false,
                      top ? Icons.expand_more : Icons.expand_less,
                      summary,
                    ),
                },
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _runToggle(
    Key key,
    String runKey,
    bool expand,
    IconData chevron,
    RunSummary summary,
  ) {
    final t = context.tokens;
    final label = summary.label;
    final style = TextStyle(
      fontFamily: monoFamily,
      fontSize: 11,
      color: t.textDim,
    );
    return Align(
      alignment: Alignment.centerLeft,
      child: Semantics(
        button: true,
        label: expand ? label : 'collapse, $label',
        excludeSemantics: true,
        child: InkWell(
          key: key,
          borderRadius: BorderRadius.circular(4),
          onTap: () {
            setState(() {
              _overrides[runKey] = expand;
              _rowsEntries = null;
            });
            if (expand) _revealRun(runKey);
          },
          child: Padding(
            padding: const EdgeInsets.symmetric(vertical: 4),
            child: Row(
              mainAxisSize: MainAxisSize.min,
              children: [
                Icon(chevron, size: 16, color: t.textDim),
                const SizedBox(width: 8),
                // Pairs wrap, not overflow: a run of every kind is wider
                // than a narrow phone.
                Flexible(
                  child: Wrap(
                    spacing: 10,
                    runSpacing: 2,
                    children: [
                      for (final (icon, n) in [
                        if (summary.thinking > 0)
                          (Icons.lightbulb_outline, summary.thinking),
                        for (final (g, n) in summary.groups)
                          (runGroupIcon(g), n),
                        if (summary.failed > 0)
                          (Icons.error_outline, summary.failed),
                      ])
                        Row(
                          mainAxisSize: MainAxisSize.min,
                          children: [
                            Icon(icon, size: 12, color: t.textDim),
                            const SizedBox(width: 2),
                            Text('$n', style: style),
                          ],
                        ),
                    ],
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
