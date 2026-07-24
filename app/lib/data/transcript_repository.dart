import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/gateway.dart';
import '../state/transcript.dart';
import '../state/transcript_controller.dart';
import '../transport/gateway_client.dart';

/// UI-facing entry point for live transcript subscriptions. Screens open a
/// subscription through this abstraction instead of constructing a
/// [TranscriptController] against the raw client, which keeps the imperative
/// subscribe/unsubscribe lifecycle out of the widgets and makes it fakeable.
abstract class TranscriptRepository {
  /// Opens a live subscription that feeds [store]. Returns null when there is
  /// no connection yet (e.g. hermetic tests or before the first connect); the
  /// caller re-opens on reconnect.
  TranscriptSubscription? open({
    required String sessionId,
    String? agentId,
    required TranscriptNotifier store,
  });
}

/// [TranscriptRepository] backed by the gateway connection. Resolves the client
/// fresh on each open so reconnects use the new client.
class TranscriptRepositoryRemote implements TranscriptRepository {
  TranscriptRepositoryRemote(this._clientOf);
  final GatewayClient? Function() _clientOf;

  @override
  TranscriptSubscription? open({
    required String sessionId,
    String? agentId,
    required TranscriptNotifier store,
  }) {
    final client = _clientOf();
    if (client == null) return null;
    return TranscriptController(
      client: client,
      store: store,
      sessionId: sessionId,
      agentId: agentId,
    )..start();
  }
}

final transcriptRepositoryProvider = Provider<TranscriptRepository>(
  (ref) => TranscriptRepositoryRemote(() => ref.read(gatewayProvider)?.client),
);
