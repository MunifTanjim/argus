import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/enums.dart';
import '../models/session.dart';

/// Whether a session runs or waits for input: the sessions that the
/// active-only view keeps.
bool isActiveSession(Session s) => switch (s.status) {
  SessionStatus.discovered ||
  SessionStatus.starting ||
  SessionStatus.working ||
  SessionStatus.awaitingInput => true,
  _ => false,
};

/// Whether the session lists show only active and awaiting-input sessions.
/// Kept in memory: every app start shows all sessions.
class ActiveOnlyController extends Notifier<bool> {
  @override
  bool build() => false;

  void toggle() => state = !state;
}

final activeOnlyProvider = NotifierProvider<ActiveOnlyController, bool>(
  ActiveOnlyController.new,
);

/// Whether [query], case-insensitively, is in a field that the session card
/// shows.
bool matchesSessionQuery(Session s, String query) {
  if (query.isEmpty) return true;
  final q = query.toLowerCase();
  return [
    s.name,
    s.summary?.task,
    s.repo,
    s.branch,
    s.nodeLabel,
  ].any((f) => f != null && f.toLowerCase().contains(q));
}

/// The session lists' text filter: null while the search field is closed.
/// Kept in memory, the same as the active-only filter.
class SessionSearchController extends Notifier<String?> {
  @override
  String? build() => null;

  void open() => state = '';

  void set(String query) => state = query.trim();

  void close() => state = null;
}

final sessionSearchProvider =
    NotifierProvider<SessionSearchController, String?>(
      SessionSearchController.new,
    );
