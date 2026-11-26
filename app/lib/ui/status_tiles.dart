import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/session.dart';
import '../state/status_filter.dart';
import 'theme.dart';

/// Counts of blocked, waiting and working sessions. A tap adds the status to
/// the [scope]'s filter or removes it, also at a count of 0.
class StatusTiles extends ConsumerWidget {
  const StatusTiles({super.key, required this.sessions, required this.scope});

  final Iterable<Session> sessions;

  /// The filter scope: [homeStatusScope], or a workspace ID.
  final String scope;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final counts = tileCounts(sessions);
    final selected = ref.watch(statusFilterProvider(scope));
    final t = context.tokens;
    final tiles = {
      TileStatus.blocked: ('BLOCKED', t.blocked),
      TileStatus.waiting: ('WAITING', t.waiting),
      TileStatus.working: ('WORKING', t.working),
    };
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 12, 12, 0),
      child: Row(
        children: [
          for (final (i, e) in tiles.entries.indexed) ...[
            if (i > 0) const SizedBox(width: 8),
            Expanded(
              child: _Tile(
                key: Key('tile-${e.key.name}'),
                label: e.value.$1,
                color: e.value.$2,
                count: counts[e.key]!,
                active: selected.contains(e.key),
                onTap: () =>
                    ref.read(statusFilterProvider(scope).notifier).toggle(e.key),
              ),
            ),
          ],
        ],
      ),
    );
  }
}

class _Tile extends StatelessWidget {
  const _Tile({
    super.key,
    required this.label,
    required this.color,
    required this.count,
    required this.active,
    required this.onTap,
  });

  final String label;
  final Color color;
  final int count;
  final bool active;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final radius = BorderRadius.circular(t.radius);
    // A filter toggle: tell a screen reader whether it is on.
    return Semantics(
      button: true,
      selected: active,
      child: InkWell(
        borderRadius: radius,
        onTap: onTap,
        child: Container(
          padding: const EdgeInsets.symmetric(vertical: 10, horizontal: 12),
          decoration: BoxDecoration(
            borderRadius: radius,
            border: Border.all(color: active ? color : t.line),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text(
                '$count',
                style: mono.copyWith(
                  fontSize: 22,
                  fontWeight: FontWeight.w700,
                  color: count == 0 ? t.textDim : color,
                ),
              ),
              Text(
                label,
                style: mono.copyWith(fontSize: 10, color: t.textDim),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
