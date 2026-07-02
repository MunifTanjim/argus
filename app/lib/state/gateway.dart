import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/legacy.dart';

import '../models/registry_event.dart';
import '../pairing/gateway_store.dart';
import '../pairing/pairing_uri.dart';
import '../transport/connection.dart';
import '../transport/jsonrpc.dart';
import '../transport/rpc_client.dart';
import '../transport/ssh_gateway.dart';
import '../transport/ssh_hostkey_store.dart';
import '../transport/ssh_key_store.dart';
import '../transport/ssh_ws_link.dart';
import '../transport/ws_link.dart';
import 'profiles.dart';
import 'push.dart';
import 'sessions.dart';

final credentialsProvider = StateProvider<GatewayCredentials?>((ref) => null);

final sshKeyStoreProvider =
    Provider<SshKeyStore>((ref) => SshKeyStore(const FlutterSecureKv()));

final hostKeyStoreProvider =
    Provider<HostKeyStore>((ref) => HostKeyStore(const FlutterSecureKv()));

/// Build the right link for a credential set: SSH gateways tunnel first, plain
/// ws/wss connect directly. Top-level so it is unit-testable without Riverpod.
Future<RpcLink> connectForCredentials(
  GatewayCredentials creds,
  SshKeyStore keyStore,
  HostKeyStore hostKeys,
) async {
  if (isSshGatewayUrl(creds.url)) {
    final key = await keyStore.load();
    if (key == null) {
      throw StateError('SSH gateway configured but no SSH key stored');
    }
    return SshWebSocketRpcLink.connect(creds, key, hostKeys);
  }
  return WebSocketRpcLink.connect(creds);
}

final connStateProvider =
    StateProvider<ConnState>((ref) => ConnState.disconnected);

/// The user-facing message behind [ConnState.failed] (e.g. a changed host key /
/// possible MITM), or null when not in a failed state.
final connErrorProvider = StateProvider<String?>((ref) => null);

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

  // Capture everything from ref at build time. Manager callbacks and onDispose
  // run after async gaps / during disposal, where touching ref throws
  // ("Cannot use Ref after it has been disposed").
  final store = ref.read(sessionsProvider.notifier);
  final keyStore = ref.read(sshKeyStoreProvider);
  final hostKeys = ref.read(hostKeyStoreProvider);
  final push = ref.read(pushControllerProvider);
  final connState = ref.read(connStateProvider.notifier);
  final connError = ref.read(connErrorProvider.notifier);
  final profileStore = ref.read(profileStoreProvider);
  final testResults = ref.read(connectionTestResultsProvider.notifier);
  final manager = ConnectionManager(
    connect: () => connectForCredentials(creds, keyStore, hostKeys),
    onConnected: (client) async {
      await loadSessions(client, store);
      client.notifications.listen((m) => dispatchEvent(m, store));
      // Register this device's push target now the connection is up (re-runs on
      // every reconnect, refreshing the gateway's record).
      push.attach(client);
      // A real successful connect verifies the active profile → green dot.
      await markActiveConnected(profileStore, testResults);
    },
  );
  // A late state event (e.g. from manager.stop() during disposal) is delivered
  // as a microtask after onDispose; cancel the sub so it never fires post-dispose.
  final statesSub = manager.states.listen((s) {
    connState.state = s;
    connError.state = s == ConnState.failed ? manager.failureMessage : null;
  });
  ref.onDispose(() {
    statesSub.cancel();
    // Tell the outgoing gateway to stop pushing before the link closes, so only
    // the active connection delivers notifications. Non-blocking, best-effort.
    push.unregisterFromCurrentGateway();
    // Drop the previous gateway's sessions so a switch/disconnect doesn't show
    // stale data until the next fetch. (Transient reconnects reuse the same
    // manager, so this doesn't fire on network blips.)
    store.clear();
    manager.stop();
  });
  manager.start();
  return manager;
});
