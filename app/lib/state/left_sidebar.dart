import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/legacy.dart';

import '../pairing/gateway_store.dart';
import 'navigation.dart';
import 'sessions.dart';

enum LeftSidebarTab { projects, sessions }

/// Saves the left sidebar's tab on this device.
class LeftSidebarTabStore {
  LeftSidebarTabStore([this._kv = const FlutterSecureKv()]);
  final SecureKv _kv;

  static const _key = 'leftSidebar.tab';

  Future<LeftSidebarTab> load() async {
    final v = await _kv.read(_key);
    return LeftSidebarTab.values.firstWhere(
      (t) => t.name == v,
      orElse: () => LeftSidebarTab.projects,
    );
  }

  Future<void> save(LeftSidebarTab t) => _kv.write(_key, t.name);
}

final leftSidebarTabStoreProvider = Provider<LeftSidebarTabStore>(
  (ref) => LeftSidebarTabStore(),
);

/// The left sidebar's tab, remembered across app starts.
class LeftSidebarTabController extends Notifier<LeftSidebarTab> {
  @override
  LeftSidebarTab build() {
    // Hydrate async.
    _load();
    return LeftSidebarTab.projects;
  }

  // Set by a choice made while the load runs, so the load does not undo it.
  var _chosen = false;

  Future<void> _load() async {
    try {
      final saved = await ref.read(leftSidebarTabStoreProvider).load();
      if (!_chosen) state = saved;
    } catch (_) {
      // Keep the default on read failure (e.g. secure storage unavailable).
    }
  }

  Future<void> set(LeftSidebarTab t) async {
    _chosen = true;
    state = t;
    try {
      await ref.read(leftSidebarTabStoreProvider).save(t);
    } catch (_) {
      // Persist failure is non-fatal.
    }
  }
}

final leftSidebarTabProvider =
    NotifierProvider<LeftSidebarTabController, LeftSidebarTab>(
      LeftSidebarTabController.new,
    );

/// The Sessions tab's filter text, apart from the Projects filter.
final sidebarSessionsFilterProvider = StateProvider<String>((ref) => '');

/// Whether the Sessions tab lists only the shown workspace's sessions.
final sidebarSessionsWorkspaceOnlyProvider = StateProvider<bool>(
  (ref) => false,
);

final shownWorkspaceProvider = Provider<String?>((ref) {
  final id = ref.watch(shownSessionProvider);
  if (id == null) return null;
  final ws = ref.watch(sessionsProvider.select((s) => s[id]?.workspaceId));
  return (ws?.isNotEmpty ?? false) ? ws : null;
});

/// The workspace the Sessions tab is narrowed to, or null when it lists all.
final sidebarSessionsWorkspaceFilterProvider = Provider<String?>(
  (ref) => ref.watch(sidebarSessionsWorkspaceOnlyProvider)
      ? ref.watch(shownWorkspaceProvider)
      : null,
);
