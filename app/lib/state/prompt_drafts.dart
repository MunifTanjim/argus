import 'package:flutter_riverpod/flutter_riverpod.dart';

/// In-memory idle-reply drafts, keyed by session id. A draft survives closing
/// and reopening the respond sheet, and is dropped on send or app restart.
class PromptDraftsNotifier extends Notifier<Map<String, String>> {
  @override
  Map<String, String> build() => const {};

  String get(String sessionId) => state[sessionId] ?? '';

  void set(String sessionId, String text) {
    if (text.isEmpty) {
      clear(sessionId);
      return;
    }
    state = {...state, sessionId: text};
  }

  void clear(String sessionId) {
    if (!state.containsKey(sessionId)) return;
    state = {...state}..remove(sessionId);
  }

  /// Drops drafts whose session id is not in [liveIds], so the store does not
  /// retain text for sessions that left the registry.
  void retain(Iterable<String> liveIds) {
    if (state.isEmpty) return;
    final live = liveIds.toSet();
    final next = {
      for (final e in state.entries)
        if (live.contains(e.key)) e.key: e.value,
    };
    if (next.length != state.length) state = next;
  }
}

final promptDraftsProvider =
    NotifierProvider<PromptDraftsNotifier, Map<String, String>>(
  PromptDraftsNotifier.new,
);
