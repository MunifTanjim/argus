import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/enums.dart';
import '../models/session.dart';

/// The statuses that the home count tiles show and filter by.
enum TileStatus { blocked, waiting, working }

/// The tile that counts [s]: null for sessions that no tile counts.
TileStatus? tileStatusOf(Session s) {
  if (s.blocked) return TileStatus.blocked;
  return switch (s.status) {
    SessionStatus.awaitingInput => TileStatus.waiting,
    SessionStatus.working || SessionStatus.starting => TileStatus.working,
    _ => null,
  };
}

Map<TileStatus, int> tileCounts(Iterable<Session> sessions) {
  final counts = {for (final t in TileStatus.values) t: 0};
  for (final s in sessions) {
    final t = tileStatusOf(s);
    if (t != null) counts[t] = counts[t]! + 1;
  }
  return counts;
}

/// The home list's status filter: null shows every status.
/// Kept in memory, the same as the active-only filter.
class StatusFilterController extends Notifier<TileStatus?> {
  @override
  TileStatus? build() => null;

  void toggle(TileStatus t) => state = state == t ? null : t;

  void clear() => state = null;
}

final statusFilterProvider =
    NotifierProvider<StatusFilterController, TileStatus?>(
      StatusFilterController.new,
    );
