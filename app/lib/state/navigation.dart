import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/legacy.dart';

/// Selected tab in HomeShell: 0 = Sessions, 1 = History, 2 = Settings.
final homeTabProvider = StateProvider<int>((ref) => 0);

const homeTabSessions = 0;

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
