import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/legacy.dart';

import '../push/push_controller.dart';

/// Holds the session id a tapped notification wants to open. HomeShell watches
/// this and navigates to the session, then clears it.
final pendingPushSessionProvider = StateProvider<String?>((ref) => null);

/// The single PushController for the app. Materialized at startup; it sets the
/// pending session on a notification tap so the UI can deep-link.
final pushControllerProvider = Provider<PushController>((ref) {
  final controller = PushController(
    onSessionTap: (id) =>
        ref.read(pendingPushSessionProvider.notifier).state = id,
  );
  controller.init();
  ref.onDispose(controller.dispose);
  return controller;
});
