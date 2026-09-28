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

void showHomeSessions(WidgetRef ref) {
  ref.read(scopeProvider.notifier).state = null;
  ref.read(homeTabProvider.notifier).state = HomeTab.sessions;
}
