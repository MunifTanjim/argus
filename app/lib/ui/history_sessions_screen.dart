import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/history_repository.dart';
import '../models/agent.dart';
import '../models/history.dart';
import 'filter_title.dart';
import 'fold_header.dart';
import 'history_details.dart';
import 'history_format.dart';
import 'history_transcript_screen.dart';
import 'responsive.dart';
import 'theme.dart';

class HistorySessionsScreen extends ConsumerStatefulWidget {
  const HistorySessionsScreen({super.key, required this.project});

  final HistoryProject project;

  @override
  ConsumerState<HistorySessionsScreen> createState() =>
      _HistorySessionsScreenState();
}

class _HistorySessionsScreenState extends ConsumerState<HistorySessionsScreen> {
  List<HistorySession> _items = [];
  bool _hasMore = false;
  bool _loading = false;
  Object? _error;

  /// The session filter: null while the filter field is closed.
  String? _query;

  /// Days whose sessions are hidden, by [_dayKey], so a fold stays on its
  /// date when "Today" turns into "Yesterday". Not stored.
  final _foldedDays = <String>{};

  /// Each loaded session's local last-activity time, parsed once: null when
  /// it does not parse.
  final _times = <HistorySession, DateTime?>{};

  DateTime? _timeOf(HistorySession s) => _times.putIfAbsent(
    s,
    () => DateTime.tryParse(s.lastActivity)?.toLocal(),
  );

  @override
  void initState() {
    super.initState();
    _loadMore(0);
  }

  Future<void> _loadMore(int offset) async {
    setState(() {
      _loading = true;
      _error = null;
    });
    final result = await ref
        .read(historyRepositoryProvider)
        .sessions(
          nodeId: widget.project.nodeId,
          projectDir: widget.project.projectDir,
          limit: 100,
          offset: offset,
        );
    if (!mounted) return;
    setState(() {
      switch (result) {
        case Ok(:final value):
          if (offset == 0) {
            _times.clear();
            _items = value.items;
          } else {
            _items = [..._items, ...value.items];
          }
          _hasMore = value.hasMore;
        case Error(:final error):
          _error = error;
      }
      _loading = false;
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(
        title: FilterTitle(
          query: _query,
          hint: 'Filter sessions',
          onChanged: (q) => setState(() => _query = q),
          onClose: () => setState(() => _query = null),
          child: HistoryTitle(
            text: widget.project.label,
            onTap: () => showHistoryProjectDetails(context, widget.project),
          ),
        ),
        actions: [
          FilterButton(
            query: _query,
            tooltip: 'Filter sessions',
            onOpen: () => setState(() => _query = ''),
          ),
        ],
      ),
      // top: false — AppBar covers the top; bottom-only clears the nav bar.
      body: SafeArea(top: false, child: _buildBody()),
    );
  }

  Widget _buildBody() {
    final error = _error;
    if (error != null) {
      return Center(child: Text(error.toString()));
    }

    if (_loading && _items.isEmpty) {
      return const Center(child: CircularProgressIndicator());
    }

    if (_items.isEmpty) {
      return const Center(child: Text('No sessions in this project.'));
    }

    final query = _query ?? '';
    final shown =
        _items.where((s) => matchesHistorySession(s, query)).toList();
    if (shown.isEmpty && !_hasMore) {
      return const Center(child: Text('No sessions match.'));
    }

    final distinctAgents =
        _items.map((s) => s.agent).where((a) => a.isNotEmpty).toSet();
    final showAgent = distinctAgents.length > 1;
    // A filter shows every match, folded or not.
    final rows = _dayRows(
      shown,
      DateTime.now(),
      timeOf: _timeOf,
      folded: query.isEmpty ? _foldedDays : const <String>{},
      hasMore: _hasMore,
    );

    return CenteredBody(
      child: ListView.builder(
        itemCount: rows.length,
        itemBuilder: (context, index) => switch (rows[index]) {
          _DayHeader(:final day, :final label, :final count, :final folded) =>
            FoldHeader(
              key: Key('history-day-$label'),
              label: label,
              count: count,
              folded: folded,
              onTap: () => setState(() {
                if (!_foldedDays.remove(day)) _foldedDays.add(day);
              }),
            ),
          _SessionEntry(:final session, :final at) => _SessionCard(
            session: session,
            at: at,
            showAgent: showAgent,
            onTap: () => Navigator.push(
              context,
              MaterialPageRoute(
                builder: (_) => HistoryTranscriptScreen(
                  session: session,
                  project: widget.project,
                ),
              ),
            ),
          ),
          _LoadMore() => Padding(
            padding: const EdgeInsets.all(16),
            child: Center(
              child: TextButton(
                onPressed: _loading ? null : () => _loadMore(_items.length),
                child: const Text('Load more'),
              ),
            ),
          ),
        },
      ),
    );
  }
}

/// A flattened row in the session list.
sealed class _Row {}

class _DayHeader extends _Row {
  _DayHeader(this.day, this.label, this.count, {required this.folded});

  /// The day's [_dayKey].
  final String day;
  final String label;
  final int count;
  final bool folded;
}

class _SessionEntry extends _Row {
  _SessionEntry(this.session, this.at);
  final HistorySession session;

  /// The local last-activity time, or null when it does not parse.
  final DateTime? at;
}

class _LoadMore extends _Row {}

/// The header over sessions whose time does not parse.
const _noDate = 'No date';

/// The local calendar date of [at], "2026-1-5", which names a day group
/// for its whole life, unlike its label.
String _dayKey(DateTime at) => '${at.year}-${at.month}-${at.day}';

/// Lays out [sessions] (newest first) under a header per local day, then the
/// sessions with no readable time under [_noDate]. A day in [folded] keeps
/// only its header.
List<_Row> _dayRows(
  List<HistorySession> sessions,
  DateTime now, {
  required DateTime? Function(HistorySession) timeOf,
  required Set<String> folded,
  required bool hasMore,
}) {
  final order = <String>[];
  final labels = <String, String>{};
  final days = <String, List<_SessionEntry>>{};
  final undated = <_SessionEntry>[];
  for (final s in sessions) {
    final at = timeOf(s);
    if (at == null) {
      undated.add(_SessionEntry(s, null));
      continue;
    }
    final day = _dayKey(at);
    if (!days.containsKey(day)) {
      order.add(day);
      labels[day] = dayLabel(at, now);
    }
    (days[day] ??= []).add(_SessionEntry(s, at));
  }
  if (undated.isNotEmpty) {
    order.add(_noDate);
    labels[_noDate] = _noDate;
    days[_noDate] = undated;
  }
  final rows = <_Row>[];
  for (final day in order) {
    final group = days[day]!;
    final isFolded = folded.contains(day);
    rows.add(_DayHeader(day, labels[day]!, group.length, folded: isFolded));
    if (!isFolded) rows.addAll(group);
  }
  return [...rows, if (hasMore) _LoadMore()];
}

class _SessionCard extends StatelessWidget {
  const _SessionCard({
    required this.session,
    required this.at,
    required this.showAgent,
    required this.onTap,
  });

  final HistorySession session;
  final DateTime? at;
  final bool showAgent;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = session;
    final turns = s.turnCount;
    final meta = [
      if (turns > 0) turns == 1 ? '1 turn' : '$turns turns',
      compactDuration(s.durationMs),
      if (showAgent && s.agent.isNotEmpty) agentLabel(s.agent),
    ].where((p) => p.isNotEmpty).join(' · ');

    final hasTitle = s.title?.isNotEmpty ?? false;
    final hasFirst = s.firstMessage?.isNotEmpty ?? false;
    final title = hasTitle || !hasFirst
        ? Text(s.displayTitle, maxLines: 1, overflow: TextOverflow.ellipsis)
        : Text.rich(
            TextSpan(children: [
              TextSpan(text: '“', style: TextStyle(color: t.textDim)),
              TextSpan(text: s.firstMessage),
            ]),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          );
    final at = this.at;

    return ListTile(
      title: Row(
        children: [
          Expanded(child: title),
          if (at != null) ...[
            const SizedBox(width: 8),
            Text(clockTime(at),
                style: TextStyle(fontSize: 11, color: t.textDim)),
          ],
        ],
      ),
      subtitle: meta.isEmpty ? null : Text(meta),
      onTap: onTap,
    );
  }
}
