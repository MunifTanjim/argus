import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/history_repository.dart';
import '../models/history.dart';
import 'agent_badge.dart';
import 'history_transcript_screen.dart';
import 'relative_time.dart';
import 'responsive.dart';

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
      appBar: AppBar(title: Text(widget.project.label)),
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

    final distinctAgents =
        _items.map((s) => s.agent).where((a) => a.isNotEmpty).toSet();
    final showAgent = distinctAgents.length > 1;

    return CenteredBody(
      child: ListView.builder(
        itemCount: _items.length + (_hasMore ? 1 : 0),
        itemBuilder: (context, index) {
          if (index == _items.length) {
            return Padding(
              padding: const EdgeInsets.all(16),
              child: Center(
                child: TextButton(
                  onPressed: _loading ? null : () => _loadMore(_items.length),
                  child: const Text('Load more'),
                ),
              ),
            );
          }
          final s = _items[index];
          return _SessionCard(
            session: s,
            label: s.displayTitle,
            showAgent: showAgent,
            onTap: () => Navigator.push(
              context,
              MaterialPageRoute(
                builder: (_) =>
                    HistoryTranscriptScreen(session: s, project: widget.project),
              ),
            ),
          );
        },
      ),
    );
  }
}

class _SessionCard extends StatelessWidget {
  const _SessionCard({
    required this.session,
    required this.label,
    required this.showAgent,
    required this.onTap,
  });

  final HistorySession session;
  final String label;
  final bool showAgent;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final subtitleParts = <String>[
      if (session.modelName != null && session.modelName!.isNotEmpty)
        session.modelName!,
      if (session.turnCount > 0) '${session.turnCount} turns',
      if (session.lastActivity.isNotEmpty) relativeTime(session.lastActivity),
    ];

    return ListTile(
      title: Text(label),
      subtitle: subtitleParts.isNotEmpty
          ? Text(subtitleParts.join(' · '))
          : null,
      trailing: showAgent && session.agent.isNotEmpty
          ? AgentBadge(agent: session.agent)
          : null,
      onTap: onTap,
    );
  }
}
