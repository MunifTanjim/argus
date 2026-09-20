import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/registry_event.dart';
import '../models/session.dart';
import 'prompt_drafts.dart';

List<Session> parseSessions(Object? raw) => (raw as List? ?? const [])
    .map((e) => Session.fromJson(e as Map<String, dynamic>))
    .toList();

Map<String, Session> applyEvent(Map<String, Session> prev, RegistryEvent ev) {
  final next = Map<String, Session>.of(prev);
  if (ev.type == RegistryEventType.removed) {
    next.remove(ev.session.id);
  } else {
    next[ev.session.id] = ev.session;
  }
  return next;
}

class SessionsNotifier extends Notifier<Map<String, Session>> {
  @override
  Map<String, Session> build() => const {};

  void _pruneDrafts() =>
      ref.read(promptDraftsProvider.notifier).retain(state.keys);

  void replaceAll(Iterable<Session> sessions) {
    state = {for (final s in sessions) s.id: s};
    _pruneDrafts();
  }

  void clear() {
    state = const {};
    _pruneDrafts();
  }

  /// Replaces only [nodeId]'s slice: drops that node's current sessions, then
  /// inserts [sessions]. Other nodes' sessions are untouched — enables progressive
  /// per-node loading without blanking the list.
  void mergeNode(String nodeId, Iterable<Session> sessions) {
    final next = <String, Session>{
      for (final e in state.entries)
        if (e.value.nodeId != nodeId) e.key: e.value,
    };
    for (final s in sessions) next[s.id] = s;
    state = next;
    _pruneDrafts();
  }

  /// Drops sessions whose origin node is not in [nodeIds] — clears sessions from
  /// nodes no longer connected, without touching the rest.
  void retainNodes(Set<String> nodeIds) {
    state = <String, Session>{
      for (final e in state.entries)
        if (e.value.nodeId != null && nodeIds.contains(e.value.nodeId))
          e.key: e.value,
    };
    _pruneDrafts();
  }

  void apply(RegistryEvent ev) {
    state = applyEvent(state, ev);
    _pruneDrafts();
  }

  void remove(String id) {
    if (!state.containsKey(id)) return;
    final next = Map<String, Session>.of(state)..remove(id);
    state = next;
    _pruneDrafts();
  }

  void put(Session s) {
    state = Map<String, Session>.of(state)..[s.id] = s;
  }
}

final sessionsProvider =
    NotifierProvider<SessionsNotifier, Map<String, Session>>(
      SessionsNotifier.new,
    );
