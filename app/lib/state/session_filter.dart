import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/enums.dart';
import '../models/session.dart';
import '../pairing/gateway_store.dart';
import 'grouping.dart';

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

/// Saves the home sessions list's grouping on this device.
class GroupByStore {
  GroupByStore([this._kv = const FlutterSecureKv()]);
  final SecureKv _kv;

  static const _key = 'sessions.groupBy';

  Future<GroupBy> load() async {
    final v = await _kv.read(_key);
    return GroupBy.values.firstWhere(
      (g) => g.name == v,
      orElse: () => GroupBy.host,
    );
  }

  Future<void> save(GroupBy g) => _kv.write(_key, g.name);
}

final groupByStoreProvider = Provider<GroupByStore>((ref) => GroupByStore());

/// The home sessions list's grouping, remembered across app starts.
class GroupByController extends Notifier<GroupBy> {
  @override
  GroupBy build() {
    // Hydrate async.
    _load();
    return GroupBy.host;
  }

  // Set by a choice made while the load runs, so the load does not undo it.
  var _chosen = false;

  Future<void> _load() async {
    try {
      final saved = await ref.read(groupByStoreProvider).load();
      if (!_chosen) state = saved;
    } catch (_) {
      // Keep the default on read failure (e.g. secure storage unavailable).
    }
  }

  Future<void> set(GroupBy g) async {
    _chosen = true;
    state = g;
    try {
      await ref.read(groupByStoreProvider).save(g);
    } catch (_) {
      // Persist failure is non-fatal.
    }
  }
}

final groupByProvider = NotifierProvider<GroupByController, GroupBy>(
  GroupByController.new,
);
