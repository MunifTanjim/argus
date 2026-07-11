import 'package:flutter_riverpod/legacy.dart';

/// Selected tab in HomeShell: 0 = Sessions, 1 = History, 2 = Settings.
final homeTabProvider = StateProvider<int>((ref) => 0);

const homeTabSessions = 0;
