import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/legacy.dart';

import '../models/registry_event.dart';
import '../pairing/pairing_uri.dart';
import '../transport/connection.dart';
import '../transport/jsonrpc.dart';
import '../transport/rpc_client.dart';
import '../transport/ws_link.dart';
import 'push.dart';
import 'sessions.dart';

final credentialsProvider = StateProvider<GatewayCredentials?>((ref) => null);

final connStateProvider =
    StateProvider<ConnState>((ref) => ConnState.disconnected);

Future<void> loadSessions(RpcClient client, SessionsNotifier store) async {
  final result = await client.call('sessions.list');
  store.replaceAll(parseSessions(result));
}

Future<void> refreshSessions(RpcClient client, SessionsNotifier store) async {
  final result = await client.call('sessions.refresh');
  store.replaceAll(parseSessions(result));
}

void dispatchEvent(RpcMessage m, SessionsNotifier store) {
  if (m.method != 'session.event') return;
  store.apply(RegistryEvent.fromJson(m.params as Map<String, dynamic>));
}

final gatewayProvider = Provider<ConnectionManager?>((ref) {
  final creds = ref.watch(credentialsProvider);
  if (creds == null) return null;

  final store = ref.read(sessionsProvider.notifier);
  final manager = ConnectionManager(
    connect: () => WebSocketRpcLink.connect(creds),
    onConnected: (client) async {
      await loadSessions(client, store);
      client.notifications.listen((m) => dispatchEvent(m, store));
      // Register this device's push target now the connection is up (re-runs on
      // every reconnect, refreshing the gateway's record).
      ref.read(pushControllerProvider).attach(client);
    },
  );
  manager.states.listen((s) {
    ref.read(connStateProvider.notifier).state = s;
  });
  ref.onDispose(manager.stop);
  manager.start();
  return manager;
});
