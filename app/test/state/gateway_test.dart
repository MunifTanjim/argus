import 'dart:async';
import 'dart:convert';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/data/client_identity_store.dart';
import 'package:argus/data/trust_chain_store.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/pairing_uri.dart';
import 'package:argus/pairing/profile_store.dart';
import 'package:argus/push/push_controller.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/profiles.dart';
import 'package:argus/state/push.dart';
import 'package:argus/state/sessions.dart';
import 'package:argus/transport/gateway_client.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/transport/rpc_client.dart';
import 'package:argus/transport/ssh_hostkey_store.dart';
import 'package:argus/transport/ssh_key_store.dart';

const _s1 =
    '{"id":"mac:%1","agent":"t","status":"working","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"node_label":"mac"}';

class _MemKv implements SecureKv {
  final _m = <String, String>{};
  @override
  Future<String?> read(String k) async => _m[k];
  @override
  Future<void> write(String k, String v) async => _m[k] = v;
  @override
  Future<void> delete(String k) async => _m.remove(k);
}

// Minimal PushController that avoids platform-channel calls in init().
class _FakePushController extends PushController {
  @override
  Future<void> init() async {}
  @override
  void attach(GatewayClient client) {}
  @override
  Future<void> unregisterFromCurrentGateway() async {}
}

void main() {
  test('loadSessions populates the store from sessions.list', () async {
    final incoming = StreamController<RpcMessage>();
    final sent = <String>[];
    final client = RpcClient(incoming: incoming.stream, sendFrame: sent.add);
    addTearDown(client.close);

    final container = ProviderContainer();
    addTearDown(container.dispose);
    final store = container.read(sessionsProvider.notifier);

    final fut = loadSessions(client, store);
    final id = (jsonDecode(sent.single.trim()) as Map)['id'] as String;
    incoming.add(
      RpcMessage.fromJson(
        jsonDecode('{"jsonrpc":"2.0","id":"$id","result":[$_s1]}'),
      ),
    );
    await fut;

    expect(container.read(sessionsProvider).containsKey('mac:%1'), isTrue);
  });

  test('dispatchEvent applies session.event, ignores others', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final store = container.read(sessionsProvider.notifier);

    dispatchEvent(
      RpcMessage.fromJson(
        jsonDecode(
          '{"jsonrpc":"2.0","method":"session.event","params":{"type":"added","session":$_s1}}',
        ),
      ),
      store,
    );
    expect(container.read(sessionsProvider).containsKey('mac:%1'), isTrue);

    dispatchEvent(
      RpcMessage.fromJson(
        jsonDecode('{"jsonrpc":"2.0","method":"transcript.delta","params":{}}'),
      ),
      store,
    );
    expect(container.read(sessionsProvider).length, 1);
  });

  test(
    'gatewayProvider onDispose defers provider resets; no Riverpod assertion',
    () async {
      final kv = _MemKv();
      final container = ProviderContainer(
        overrides: [
          sshKeyStoreProvider.overrideWithValue(SshKeyStore(kv)),
          hostKeyStoreProvider.overrideWithValue(HostKeyStore(kv)),
          clientIdentityStoreProvider.overrideWithValue(
            ClientIdentityStore(kv),
          ),
          trustChainStoreProvider.overrideWithValue(TrustChainStore(kv)),
          profileStoreProvider.overrideWithValue(ProfileStore(kv)),
          // Bypass platform-channel calls in PushController.init().
          pushControllerProvider.overrideWithValue(_FakePushController()),
        ],
      );
      addTearDown(container.dispose);

      // SSH URL + empty keyStore → connectForCredentials throws StateError
      // immediately; ConnectionManager catches it and stays in reconnecting —
      // no real I/O required.
      container
          .read(credentialsProvider.notifier)
          .state = const GatewayCredentials(
        'ssh://gw.example.ts.net',
        'tok',
        e2eEnabled: false,
      );

      // Force provider initialization.
      container.read(gatewayProvider);

      // Let the first (failing) connect attempt run.
      await Future.microtask(() {});

      // Seed state that onDispose should reset.
      container
          .read(sessionsProvider.notifier)
          .replaceAll(parseSessions([jsonDecode(_s1)]));
      container.read(equivocationProvider.notifier).state = true;

      expect(container.read(sessionsProvider), isNotEmpty);
      expect(container.read(equivocationProvider), isTrue);

      // Disconnect: setting credentials to null rebuilds gatewayProvider
      // (returning null) and disposes the old instance, triggering onDispose.
      container.read(credentialsProvider.notifier).state = null;

      // onDispose defers the resets to a microtask; pump it.
      await Future.microtask(() {});

      expect(
        container.read(sessionsProvider),
        isEmpty,
        reason: 'sessions must be cleared after gateway dispose',
      );
      expect(
        container.read(equivocationProvider),
        isFalse,
        reason: 'equivocation must be reset after gateway dispose',
      );
    },
  );
}
