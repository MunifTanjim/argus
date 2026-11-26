import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/enums.dart';
import '../models/session.dart';
import 'toggle_set.dart';

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

/// The status filter scope of the home list. A workspace list uses its
/// workspace ID.
const homeStatusScope = 'home';

/// The tiles selected in one list, keyed by scope: [homeStatusScope], or a
/// workspace ID. An empty set shows every status. Kept in memory: every app
/// start shows all sessions.
final statusFilterProvider =
    NotifierProvider.family<ToggleSet<TileStatus>, Set<TileStatus>, String>(
      (String _) => ToggleSet<TileStatus>(),
    );
