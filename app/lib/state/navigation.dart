import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/legacy.dart';

final homeTabProvider = StateProvider<HomeTab>((ref) => HomeTab.sessions);

enum HomeTab { sessions, history }

enum WorkspaceTab { sessions, changes, files }

/// The drawer selection: null is Home, else a composite workspace id.
final scopeProvider = StateProvider<String?>((ref) => null);

// Per-workspace view memory for the app process (the tab, the Files path, and
// the Changes base below); never saved to disk.
final workspaceTabProvider = StateProvider.family<WorkspaceTab, String>(
    (ref, workspaceId) => WorkspaceTab.sessions);
final filesPathProvider =
    StateProvider.family<String, String>((ref, workspaceId) => '');
final changesAgainstProvider =
    StateProvider.family<String, String>((ref, workspaceId) => '');

String parentPath(String path) {
  final i = path.lastIndexOf('/');
  return i < 0 ? '' : path.substring(0, i);
}

void showHomeSessions(ProviderContainer container) {
  container.read(scopeProvider.notifier).state = null;
  container.read(homeTabProvider.notifier).state = HomeTab.sessions;
}

/// The session each scope reopens on entry (null is Home), like the TUI view
/// memory: a session is kept under its own workspace, or Home if it has none.
/// For the app process only; never saved.
class RememberedSessions extends Notifier<Map<String?, String>> {
  @override
  Map<String?, String> build() => const {};

  void remember(String? scope, String sessionId) {
    if (state[scope] == sessionId) return;
    state = {...state, scope: sessionId};
  }

  void clear(String? scope) {
    if (!state.containsKey(scope)) return;
    state = {...state}..remove(scope);
  }

  void forget(String sessionId) => retain((_, id) => id != sessionId);

  void retain(bool Function(String? scope, String sessionId) keep) {
    final next = {
      for (final e in state.entries)
        if (keep(e.key, e.value)) e.key: e.value,
    };
    if (next.length != state.length) state = next;
  }
}

final rememberedSessionsProvider =
    NotifierProvider<RememberedSessions, Map<String?, String>>(
  RememberedSessions.new,
);
