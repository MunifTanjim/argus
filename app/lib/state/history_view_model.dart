import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/history_repository.dart';
import '../models/history.dart';

/// View model for the history projects list (the History tab). Holds the
/// projects as the UI's source of truth so the screen itself stays a stateless
/// renderer; [reload] is wired to pull-to-refresh and to reconnect.
class HistoryProjectsViewModel extends AsyncNotifier<List<HistoryProject>> {
  Future<List<HistoryProject>> _fetch() async {
    final result = await ref.read(historyRepositoryProvider).projects();
    return switch (result) {
      Ok(:final value) => value,
      Error(:final error) => throw error,
    };
  }

  @override
  Future<List<HistoryProject>> build() => _fetch();

  /// Re-fetches the projects, surfacing loading and error through [state].
  Future<void> reload() async {
    state = const AsyncValue.loading();
    state = await AsyncValue.guard(_fetch);
  }
}

final historyProjectsProvider =
    AsyncNotifierProvider<HistoryProjectsViewModel, List<HistoryProject>>(
        HistoryProjectsViewModel.new);
