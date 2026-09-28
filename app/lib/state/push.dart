import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/legacy.dart';

import '../push/push_controller.dart';

/// A session a deep-link wants to open: a tapped notification opens it in the
/// scope on screen at that time, a resume ([inHome]) always in Home.
class PendingOpen {
  const PendingOpen(this.sessionId, {this.inHome = false});

  final String sessionId;
  final bool inHome;
}

/// HomeShell watches this and navigates to the session once it is known, then
/// clears it.
final pendingOpenSessionProvider = StateProvider<PendingOpen?>((ref) => null);

/// The single PushController for the app. Materialized at startup; it sets the
/// pending session on a notification tap so the UI can deep-link.
final pushControllerProvider = Provider<PushController>((ref) {
  final controller = PushController(
    onSessionTap: (id) =>
        ref.read(pendingOpenSessionProvider.notifier).state = PendingOpen(id),
  );
  controller.init();
  ref.onDispose(controller.dispose);
  return controller;
});
