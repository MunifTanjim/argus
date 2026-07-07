import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/chunk.dart';
import '../util/id.dart';

/// Truncates [chunks] to [d.fromIndex] (clamped), then appends [d.chunks].
/// Mirrors internal/tui/transcript_stream.go applyDelta.
List<Chunk> applyDelta(List<Chunk> chunks, TranscriptDelta d) {
  final from = d.fromIndex > chunks.length ? chunks.length : d.fromIndex;
  return [...chunks.sublist(0, from), ...d.chunks];
}

String newSubId() => newHexId();

class TranscriptState {
  final String? subId;
  final List<Chunk> chunks;
  final Object? error;

  /// Whether the initial snapshot has arrived. Distinguishes "still loading"
  /// from "loaded but empty"; stays true across reconnects so re-subscribing
  /// doesn't flash a spinner over already-cached chunks.
  final bool loaded;

  const TranscriptState(
      {this.subId, this.chunks = const [], this.error, this.loaded = false});

  TranscriptState copyWith({
    String? subId,
    List<Chunk>? chunks,
    Object? error,
    bool clearError = false,
    bool? loaded,
  }) =>
      TranscriptState(
        subId: subId ?? this.subId,
        chunks: chunks ?? this.chunks,
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
  int get chunkCount => state.chunks.length;

  void setSubId(String id) =>
      state = state.copyWith(subId: id, clearError: true);

  void applyDelta(TranscriptDelta d) {
    if (d.subId != state.subId) return;
    state = state.copyWith(
        chunks: applyDelta_(state.chunks, d), clearError: true, loaded: true);
  }

  void setError(Object? e) => state = state.copyWith(error: e);

  void reset() => state = const TranscriptState();
}

// Internal alias so the method name doesn't shadow the top-level function.
List<Chunk> applyDelta_(List<Chunk> chunks, TranscriptDelta d) =>
    applyDelta(chunks, d);
