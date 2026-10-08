import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/entry.dart';
import '../util/id.dart';

/// Mirrors internal/tui/transcript_stream.go applyDelta.
List<Entry> applyDelta(List<Entry> entries, TranscriptDelta d) {
  final from = d.fromIndex > entries.length ? entries.length : d.fromIndex;
  return [...entries.sublist(0, from), ...d.entries];
}

String newSubId() => newHexId();

class TranscriptState {
  final String? subId;
  final List<Entry> entries;
  final Object? error;

  /// Whether the initial snapshot has arrived. Distinguishes "still loading"
  /// from "loaded but empty"; stays true across reconnects so re-subscribing
  /// doesn't flash a spinner over already-cached entries.
  final bool loaded;

  const TranscriptState(
      {this.subId, this.entries = const [], this.error, this.loaded = false});

  TranscriptState copyWith({
    String? subId,
    List<Entry>? entries,
    Object? error,
    bool clearError = false,
    bool? loaded,
  }) =>
      TranscriptState(
        subId: subId ?? this.subId,
        entries: entries ?? this.entries,
        error: clearError ? null : (error ?? this.error),
        loaded: loaded ?? this.loaded,
      );
}

class TranscriptNotifier extends Notifier<TranscriptState> {
  @override
  TranscriptState build() => const TranscriptState();

  /// Public reads for the controller, which lives outside the notifier and so
  /// cannot touch the protected [state] directly.
  String? get currentSubId => state.subId;
  int get entryCount => state.entries.length;

  void setSubId(String id) =>
      state = state.copyWith(subId: id, clearError: true);

  void applyDelta(TranscriptDelta d) {
    if (d.subId != state.subId) return;
    state = state.copyWith(
        entries: applyDelta_(state.entries, d), clearError: true, loaded: true);
  }

  void setError(Object? e) => state = state.copyWith(error: e);

  void reset() => state = const TranscriptState();
}

// Internal alias so the method name doesn't shadow the top-level function.
List<Entry> applyDelta_(List<Entry> entries, TranscriptDelta d) =>
    applyDelta(entries, d);
