import 'dart:async';
import 'dart:typed_data';

import 'package:flutter/foundation.dart' show visibleForTesting;
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_riverpod/legacy.dart';

import '../data/client_identity_store.dart';
import '../data/trust_chain_store.dart';
import '../e2e/e2e.dart';
import '../models/registry_event.dart';
import '../pairing/gateway_store.dart';
import '../pairing/pairing_uri.dart';
import '../transport/connection.dart';
import '../transport/jsonrpc.dart';
import '../transport/gateway_client.dart';
import '../transport/ssh_gateway.dart';
import '../transport/ssh_hostkey_store.dart';
import '../transport/ssh_key_store.dart';
import '../transport/ssh_ws_link.dart';
import '../transport/ws_link.dart';
import 'profiles.dart';
import 'push.dart';
import 'sessions.dart';

class TrustAnchorTampered implements FatalConnectError {
  @override
  String get message =>
      'Stored trust anchor failed verification — refusing to connect (possible tampering). '
      'Clear app data to re-establish trust.';
}

bool _bytesEq(List<int> a, List<int> b) {
  if (a.length != b.length) return false;
  for (var i = 0; i < a.length; i++) {
    if (a[i] != b[i]) return false;
  }
  return true;
}

/// Builds a connected E2E client over a bridged link, with identity + trust
/// persistence. TOFU on first use; a stored anchor is re-verified (fail-closed on
/// tampering — never silently re-TOFU) and re-anchors the pull; the verified chain
/// is persisted on advance.
Future<GatewayClient> buildE2EClient(
  Stream<RpcMessage> incoming,
  void Function(String) send,
  ClientIdentityStore identityStore,
  TrustChainStore chainStore,
) async {
  final identity = await identityStore.loadOrCreate();
  final Uint8List? seed;
  try {
    seed = await chainStore.load();
  } on TrustAnchorLost {
    // Anchored before, but the stored anchor is now missing/corrupt — fail closed
    // (do NOT re-TOFU onto whatever the gateway serves).
    throw TrustAnchorTampered();
  }
  if (seed != null) {
    final probe = TrustStore.tofu();
    try {
      await probe.ingest(seed);
    } catch (_) {
      throw TrustAnchorTampered(); // do NOT re-TOFU a rejected anchor
    }
  }
  final client = E2EClient(
    incoming,
    send,
    identity,
    tofu: true,
    initialTrustChain: seed,
    // Re-sync the trust log periodically so mid-session revocations take effect
    // (channels to now-unauthorized nodes are dropped), persisting each advance.
    trustResyncInterval: const Duration(seconds: 30),
    onTrustChainAdvance: chainStore.save,
  );
  await client.connect();
  final head = client.trustChainBytes;
  // If connect() throws after an in-progress trust advance, the save below is
  // skipped; the next successful reconnect re-pulls, re-advances, and persists.
  if (head != null && (seed == null || !_bytesEq(head, seed))) {
    await chainStore.save(head);
  }
  return client;
}

/// Returns whether [client] is an [E2EClient] that has detected an equivocation.
/// Extracted from the equivPoll callback in [gatewayProvider] for testability.
@visibleForTesting
bool equivocationOf(GatewayClient? client) =>
    client is E2EClient && client.equivocation;

/// Starts the equivocation poll and returns the [Timer] so the caller can cancel
/// it on dispose. It polls once immediately — so an equivocation already present
/// at connect surfaces without waiting a full [interval] — then on every tick.
/// The poll writes [equivocationOf] unconditionally (no `!equivocation.state`
/// guard) so a stale true left by a previous session is always cleared when the
/// new [E2EClient] starts clean. [interval] defaults to 30 s (the trust-resync
/// cadence); injectable for tests.
@visibleForTesting
Timer startEquivPoll(
  ConnectionManager manager,
  StateController<bool> equivocation, {
  Duration interval = const Duration(seconds: 30),
}) {
  void poll() => equivocation.state = equivocationOf(manager.client);
  poll(); // surface an existing equivocation immediately, not only after the first tick
  return Timer.periodic(interval, (_) => poll());
}

final clientIdentityStoreProvider =
    Provider<ClientIdentityStore>((ref) => ClientIdentityStore(const FlutterSecureKv()));
final trustChainStoreProvider =
    Provider<TrustChainStore>((ref) => TrustChainStore(const FlutterSecureKv()));

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

/// Whether the active E2E client has detected a trust-log equivocation.
/// Polled from the client on the trust-resync cadence; reset on disconnect.
final equivocationProvider = StateProvider<bool>((ref) => false);

Future<void> loadSessions(GatewayClient client, SessionsNotifier store) async {
  final result = await client.call('sessions.list');
  store.replaceAll(parseSessions(result));
}

Future<void> refreshSessions(GatewayClient client, SessionsNotifier store) async {
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
  final equivocation = ref.read(equivocationProvider.notifier);
  final profileStore = ref.read(profileStoreProvider);
  final testResults = ref.read(connectionTestResultsProvider.notifier);
  final identityStore = ref.read(clientIdentityStoreProvider);
  final chainStore = ref.read(trustChainStoreProvider);
  final manager = ConnectionManager(
    connect: () => connectForCredentials(creds, keyStore, hostKeys),
    clientFactory: (incoming, send) => buildE2EClient(incoming, send, identityStore, chainStore),
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
  // Poll the E2E client's equivocation flag on the trust-resync cadence so the
  // UI reflects it without requiring a connection-state change to trigger a
  // rebuild. See [startEquivPoll] for the unconditional-write rationale.
  final equivPoll = startEquivPoll(manager, equivocation);
  ref.onDispose(() {
    equivPoll.cancel();
    equivocation.state = false; // reset per-session flag on disconnect
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
