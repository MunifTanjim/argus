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
import '../transport/ssh_bridge.dart';
import '../transport/ssh_gateway.dart';
import '../transport/ssh_hostkey_store.dart';
import '../transport/ssh_key_store.dart';
import '../transport/ssh_tunnel.dart' show HostKeyChangedException;
import '../transport/ssh_ws_link.dart';
import '../transport/ws_link.dart';
import 'control.dart';
import 'device_identity.dart';
import 'profiles.dart';
import 'projects.dart';
import 'push.dart';
import 'sessions.dart';
import 'terminals.dart';

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
  TrustChainStore chainStore, {
  ChainLoader loadChain = loadChainInBackground,
  void Function(GatewayClient client)? onTrustAdvance,
  void Function(Uint8List)? sendBytes,
  DirectDialer? directDialer,
  SshOpener? sshOpener,
  NodeSshSettings? nodeSsh,
}) async {
  final identity = await identityStore.loadOrCreate();
  final Uint8List? seed;
  try {
    seed = await chainStore.load();
  } on TrustAnchorLost {
    // Anchored before, but the stored anchor is now missing/corrupt — fail closed
    // (do NOT re-TOFU onto whatever the gateway serves).
    throw TrustAnchorTampered();
  }
  final trust = TrustStore.tofu(loadChain: loadChain);
  if (seed != null) {
    try {
      await trust.ingest(seed);
    } catch (_) {
      throw TrustAnchorTampered(); // do NOT re-TOFU a rejected anchor
    }
  }
  late final E2EClient client;
  client = E2EClient(
    incoming,
    send,
    identity,
    trustStore: trust,
    sendBytes: sendBytes,
    directDialer: directDialer,
    sshOpener: sshOpener,
    sshTargets: nodeSsh?.targets ?? const {},
    sshCommand: nodeSsh?.effectiveCommand ?? defaultNodeSshCommand,
    // Re-sync the trust log periodically so mid-session revocations take effect
    // (channels to now-unauthorized nodes are dropped), persisting each advance.
    trustResyncInterval: const Duration(seconds: 30),
    onTrustChainAdvance: (chain) async {
      await chainStore.save(chain);
      onTrustAdvance?.call(client);
    },
  );
  // Channels open in the background so a session's transcript waits only for
  // its own node, not the slowest one.
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

/// Starts the trust poll on the resync cadence and returns the [Timer] so the
/// caller can cancel it on dispose. It polls once immediately — so state present
/// at connect surfaces without waiting a full [interval] — then on every tick.
/// It refreshes [equivocation] and [trustSignature]; the latter re-renders the
/// trust page on a background trust change (disable, supersession, authorization)
/// that would otherwise show only after a reconnect. Both are written
/// unconditionally so a stale value from a previous session is cleared when the
/// new [E2EClient] starts clean. [interval] defaults to 30 s; injectable for tests.
@visibleForTesting
Timer startEquivPoll(
  ConnectionManager manager,
  StateController<bool> equivocation,
  StateController<String> trustSignature, {
  Duration interval = const Duration(seconds: 30),
}) {
  void poll() {
    equivocation.state = equivocationOf(manager.client);
    trustSignature.state = trustSignatureOf(trustSummaryOf(manager.client));
  }

  // Deferred: this runs during gatewayProvider's build, and a provider may not
  // modify another during build. The microtask surfaces existing state right
  // after build, before the first frame.
  Future.microtask(poll);
  return Timer.periodic(interval, (_) => poll());
}

final clientIdentityStoreProvider = Provider<ClientIdentityStore>(
  (ref) => ClientIdentityStore(const FlutterSecureKv()),
);
final trustChainStoreProvider = Provider<TrustChainStore>(
  (ref) => TrustChainStore(const FlutterSecureKv()),
);

final credentialsProvider = StateProvider<GatewayCredentials?>((ref) => null);

/// The link each open node's channel uses (`gateway`, `tcp`, `ssh`), by node
/// id. A node without a link is absent.
final nodeViasProvider = StateProvider<Map<String, String>>((ref) => const {});

/// Retries the upgrade of every node on the relay now: the retry timer is
/// frozen while the app is in the background.
void resumeDirectLinks(GatewayClient? client) {
  if (client is E2EClient) client.retryUpgradesNow();
}

/// Dials a node's direct endpoint.
Future<RpcLink> dialDirect(String url, Duration timeout) =>
    WebSocketRpcLink.connectDirect(url, timeout: timeout);

/// The user's answers for node SSH links, implemented by the UI.
abstract interface class NodeSshPrompter {
  /// Whether to trust a node host's first-seen key: true to trust, false for
  /// an explicit decline, null when the user dismissed the prompt. Throws when
  /// it cannot ask.
  Future<bool?> confirmHostKey(String hostPort, String fingerprint);

  /// Tells the user a node host's pinned key changed; returns whether to
  /// forget the pinned key.
  Future<bool> hostKeyChanged(String hostPort, String message);
}

final nodeSshPrompterProvider = Provider<NodeSshPrompter?>((ref) => null);

/// How long a dismissed node host-key prompt stays quiet, unless the app
/// resumes first.
const nodeSshDismissCooldown = Duration(minutes: 10);

var _appResumes = 0;

/// Records that the app came back to the foreground: dismissed node host-key
/// prompts may be asked again.
void noteAppResumed() => _appResumes++;

/// Opens node SSH links with the profile's key and the pinned host keys. A
/// first-seen host key fails the dial at once and asks the user in the
/// background; once trusted, [onTrusted] retries the upgrades and the key is
/// pinned on that dial. An explicitly declined key is not asked again while
/// these credentials stay active. A dismissed prompt is not asked again for
/// [dismissCooldown] or until the app resumes. A prompt that could not be
/// shown (no UI, failed) is asked again on a later dial. A changed key is
/// reported once per host.
SshOpener nodeSshOpener({
  required Future<SshKey?> Function() loadKey,
  required HostKeyStore hostKeys,
  required NodeSshPrompter? Function() prompter,
  required void Function() onTrusted,
  SshBridgeOpen open = SshBridge.open,
  Duration dismissCooldown = nodeSshDismissCooldown,
  DateTime Function() now = DateTime.now,
  int Function() resumes = _resumeCount,
}) {
  final trusted = <String>{};
  final declined = <String>{};
  final asking = <String>{};
  final reported = <String>{};
  final dismissed = <String, ({DateTime until, int resumes})>{};

  bool quiet(String k) {
    final d = dismissed[k];
    if (d == null) return false;
    if (d.resumes == resumes() && now().isBefore(d.until)) return true;
    dismissed.remove(k);
    return false;
  }

  Future<void> ask(String hostPort, String fingerprint, String k) async {
    final p = prompter();
    bool? answer;
    var shown = p != null;
    try {
      answer = await p?.confirmHostKey(hostPort, fingerprint);
    } catch (_) {
      shown = false;
    }
    asking.remove(k);
    if (answer == true) {
      trusted.add(k);
      onTrusted();
    } else if (answer == false) {
      declined.add(k);
    } else if (shown) {
      dismissed[k] = (until: now().add(dismissCooldown), resumes: resumes());
    }
  }

  Future<void> report(String hostPort, String message) async {
    try {
      if (await prompter()?.hostKeyChanged(hostPort, message) ?? false) {
        await hostKeys.forget(hostPort);
        reported.remove(hostPort);
        onTrusted();
      }
    } catch (_) {}
  }

  return (target, command, timeout) async {
    final key = await loadKey();
    if (key == null) throw StateError('no SSH key for node links');
    final hostPort = target.hostPort;
    try {
      return await open(
        target,
        key,
        hostKeys,
        command: command,
        timeout: timeout,
        confirmHostKey: (fingerprint) async {
          final k = '$hostPort $fingerprint';
          if (trusted.contains(k)) return true;
          if (!declined.contains(k) && !quiet(k) && asking.add(k)) {
            unawaited(ask(hostPort, fingerprint, k));
          }
          return false;
        },
      );
    } on HostKeyChangedException catch (e) {
      if (reported.add(hostPort)) unawaited(report(hostPort, e.message));
      rethrow;
    }
  };
}

int _resumeCount() => _appResumes;

final sshKeyStoreProvider = Provider<SshKeyStore>(
  (ref) => SshKeyStore(const FlutterSecureKv()),
);

final hostKeyStoreProvider = Provider<HostKeyStore>(
  (ref) => HostKeyStore(const FlutterSecureKv()),
);

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

final connStateProvider = StateProvider<ConnState>(
  (ref) => ConnState.disconnected,
);

/// The user-facing message behind [ConnState.failed] (e.g. a changed host key /
/// possible MITM), or null when not in a failed state.
final connErrorProvider = StateProvider<String?>((ref) => null);

/// Whether the active E2E client has detected a trust-log equivocation.
/// Polled from the client on the trust-resync cadence; reset on disconnect.
final equivocationProvider = StateProvider<bool>((ref) => false);

/// Whether the sessions are not yet known: a dial is under way or the
/// connect-time load is in flight. An empty list then means "not loaded", not
/// "no sessions".
final sessionsLoadingProvider = StateProvider<bool>((ref) => false);

Future<void> loadSessions(GatewayClient client, SessionsNotifier store) =>
    _loadSessions(client, store, 'sessions.list');

Future<void> refreshSessions(GatewayClient client, SessionsNotifier store) =>
    _loadSessions(client, store, 'sessions.refresh');

Future<void> _loadSessions(
  GatewayClient client,
  SessionsNotifier store,
  String method,
) async {
  if (client is E2EClient) {
    // Drop disconnected nodes' sessions, keeping still-opening ones until their
    // open settles.
    store.retainNodes({...client.connectedNodeIds, ...client.openingNodeIds});
    await client.forEachNodeSessions(
      method,
      (nodeId, sessions) => store.mergeNode(nodeId, parseSessions(sessions)),
    );
    store.retainNodes(client.connectedNodeIds);
    return;
  }
  store.replaceAll(parseSessions(await client.call(method)));
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
  final projects = ref.read(projectsProvider.notifier);
  final terminals = ref.read(terminalsProvider.notifier);
  final rosterRevision = ref.read(rosterRevisionProvider.notifier);
  final nodeVias = ref.read(nodeViasProvider.notifier);
  final keyStore = ref.read(sshKeyStoreProvider);
  final hostKeys = ref.read(hostKeyStoreProvider);
  final push = ref.read(pushControllerProvider);
  final connState = ref.read(connStateProvider.notifier);
  final connError = ref.read(connErrorProvider.notifier);
  final equivocation = ref.read(equivocationProvider.notifier);
  final sessionsLoading = ref.read(sessionsLoadingProvider.notifier);
  // Bumped per connect so a superseded client's load cannot clear the flag
  // for the current one.
  var loadGen = 0;
  final trustSignature = ref.read(trustSignatureProvider.notifier);
  final profileStore = ref.read(profileStoreProvider);
  final testResults = ref.read(connectionTestResultsProvider.notifier);
  final identityStore = ref.read(clientIdentityStoreProvider);
  final chainStore = ref.read(trustChainStoreProvider);
  final keyLibrary = ref.read(keyLibraryStoreProvider);
  final prompter = ref.read(nodeSshPrompterProvider);
  ConnectionManager? current;
  final nodeSsh = creds.nodeSsh;
  final SshOpener? sshOpener = (nodeSsh == null || nodeSsh.targets.isEmpty)
      ? null
      : nodeSshOpener(
          loadKey: () async {
            final id = nodeSsh.keyId;
            final k = id == null ? null : await keyLibrary.get(id);
            return k == null ? null : SshKey(k.pem, k.passphrase);
          },
          hostKeys: hostKeys,
          prompter: () => prompter,
          onTrusted: () => resumeDirectLinks(current?.client),
        );
  final manager = ConnectionManager(
    connect: () => connectForCredentials(creds, keyStore, hostKeys),
    clientFactory: (incoming, send, sendBytes) => creds.e2eEnabled
        ? buildE2EClient(
            incoming,
            send,
            identityStore,
            chainStore,
            // Refresh the trust status now, not on the next poll, so the home
            // banner clears when the device's channels open.
            onTrustAdvance: (client) =>
                trustSignature.state = trustSignatureOf(trustSummaryOf(client)),
            sendBytes: sendBytes,
            directDialer: dialDirect,
            sshOpener: sshOpener,
            nodeSsh: nodeSsh,
          )
        : Future(() async {
            final client = E2EClient(
              incoming,
              send,
              KeyPair(Uint8List(32), Uint8List(32)),
              plaintext: true,
              sendBytes: sendBytes,
              directDialer: dialDirect,
              p2pToken: creds.token,
              sshOpener: sshOpener,
              sshTargets: nodeSsh?.targets ?? const {},
              sshCommand: nodeSsh?.effectiveCommand ?? defaultNodeSshCommand,
            );
            await client.connect();
            return client;
          }),
    onConnected: (client) async {
      if (client is E2EClient) {
        nodeVias.state = client.nodeVias;
        client.sessionReconciles.listen(
          (r) => store.mergeNode(r.nodeId, parseSessions(r.sessions)),
        );
        client.nodePaths.listen((p) {
          final prev = nodeVias.state[p.nodeId] ?? '';
          nodeVias.state = {
            for (final e in nodeVias.state.entries)
              if (e.key != p.nodeId) e.key: e.value,
            if (p.via.isNotEmpty) p.nodeId: p.via,
          };
          // The node dropped the old link's terminal attaches.
          if (prev.isNotEmpty && p.via.isNotEmpty && p.via != prev) {
            unawaited(terminals.load(client));
          }
        });
      }
      // Not awaited, so an open session subscribes as soon as the link is up
      // instead of after every node's list.
      final gen = ++loadGen;
      sessionsLoading.state = true;
      unawaited(
        loadSessions(client, store).whenComplete(() {
          if (gen == loadGen) sessionsLoading.state = false;
        }),
      );
      unawaited(projects.load(client));
      unawaited(terminals.load(client));
      client.notifications.listen((m) {
        dispatchEvent(m, store);
        if (m.method == 'node.event') rosterRevision.state++;
        if (m.method == 'project.changed' || m.method == 'node.event') {
          unawaited(projects.load(client));
        }
        if (m.method == 'terminal.changed' || m.method == 'node.event') {
          unawaited(terminals.load(client));
        }
      });
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
    if (s == ConnState.connecting) sessionsLoading.state = true;
    if (s == ConnState.failed) {
      loadGen++;
      sessionsLoading.state = false;
    }
  });
  // Start the equivocation poll only for E2E connections. For plaintext,
  // equivocationOf always returns false anyway, but we skip the timer entirely.
  final Timer? equivPoll = creds.e2eEnabled
      ? startEquivPoll(manager, equivocation, trustSignature)
      : null;
  ref.onDispose(() {
    equivPoll?.cancel();
    statesSub.cancel();
    push.unregisterFromCurrentGateway();
    manager.stop();
    // Riverpod forbids modifying other providers inside onDispose. Defer the
    // resets to a microtask that runs after teardown; both providers are
    // app-level (not autoDispose) so they persist to receive it. Resets the
    // per-session equivocation flag and drops the previous gateway's sessions so
    // a switch/disconnect does not show stale data until the next fetch.
    Future.microtask(() {
      equivocation.state = false;
      trustSignature.state = '';
      nodeVias.state = const {};
      sessionsLoading.state = false;
      store.clear();
      projects.clear();
      terminals.clear();
    });
  });
  current = manager;
  manager.start();
  return manager;
});
