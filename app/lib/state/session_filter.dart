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
