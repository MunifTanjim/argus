import 'dart:async';

import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/chunk.dart';
import '../transport/jsonrpc.dart';
import '../transport/rpc_client.dart';
import 'transcript.dart';

// Keyed by session id. The arg isn't needed inside the notifier — the
// controller drives subscribe/dispatch externally — so each key just gets its
// own isolated TranscriptNotifier instance.
final transcriptProvider =
    NotifierProvider.family<TranscriptNotifier, TranscriptState, String>(
        (String _) => TranscriptNotifier());

/// Opens (or re-opens) a subscription and seeds the store with the catch-up
/// delta. Sends have_chunks = current cached length so the server can send a
/// minimal catch-up after a reconnect.
Future<void> subscribeTranscript(
  RpcClient client,
  TranscriptNotifier store, {
  required String sessionId,
  String? agentId,
}) async {
  final sub = newSubId();
  store.setSubId(sub);
  try {
    final result = await client.call('transcript.subscribe', {
      'sub_id': sub,
      'session_id': sessionId,
      if (agentId != null && agentId.isNotEmpty) 'agent_id': agentId,
      'have_chunks': store.chunkCount,
    });
    store.applyDelta(TranscriptDelta.fromJson(result as Map<String, dynamic>));
  } catch (e) {
    store.setError(e);
  }
}

/// Routes a server push to the store. The store filters by sub_id.
void dispatchDelta(RpcMessage m, TranscriptNotifier store) {
  if (m.method != 'transcript.delta') return;
  store.applyDelta(TranscriptDelta.fromJson(m.params as Map<String, dynamic>));
}

/// An open transcript subscription that the UI disposes on teardown. Lets
/// screens hold the handle without depending on the concrete controller.
abstract class TranscriptSubscription {
  void dispose();
}

/// Owns the imperative lifecycle for one open session's transcript: notification
/// listener, (re)subscribe, and unsubscribe. The detail screen creates one of
/// these in its State and disposes it on teardown.
class TranscriptController implements TranscriptSubscription {
  TranscriptController({
    required this.client,
    required this.store,
    required this.sessionId,
    this.agentId,
  });

  final RpcClient client;
  final TranscriptNotifier store;
  final String sessionId;
  final String? agentId;

  StreamSubscription<RpcMessage>? _sub;

  void start() {
    _sub = client.notifications.listen((m) => dispatchDelta(m, store));
    subscribeTranscript(client, store, sessionId: sessionId, agentId: agentId);
  }

  @override
  void dispose() {
    _sub?.cancel();
    final sub = store.currentSubId;
    if (sub != null) {
      // best-effort; ignore failures on a possibly-dead link.
      client
          .call('transcript.unsubscribe', {'sub_id': sub}).catchError(
              (_) => null);
    }
  }
}
