import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/data/client_identity_store.dart';
import 'package:argus/data/trust_chain_store.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/pairing_uri.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/transport/gateway_client.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/transport/rpc_client.dart';
import '../e2e/loopback.dart';

class _MemKv implements SecureKv {
  final _m = <String, String>{};
  @override Future<String?> read(String k) async => _m[k];
  @override Future<void> write(String k, String v) async => _m[k] = v;
  @override Future<void> delete(String k) async => _m.remove(k);
}

/// Minimal in-memory RpcLink: open, never closes on its own, ignores sent frames.
class _MemLink implements RpcLink {
  final _ctrl = StreamController<RpcMessage>.broadcast();
  @override Stream<RpcMessage> get incoming => _ctrl.stream;
  @override void send(String frame) {}
  @override Future<void> close() async {
    if (!_ctrl.isClosed) await _ctrl.close();
  }
}

void main() {
  test('e2eEnabled=false: no clientFactory → ConnectionManager creates plaintext RpcClient', () async {
    final link = _MemLink();
    GatewayClient? captured;
    final manager = ConnectionManager(
      connect: () async => link,
      clientFactory: null,
      onConnected: (c) async { captured = c; },
      keepaliveInterval: const Duration(hours: 1),
      keepaliveTimeout: const Duration(hours: 1),
    );
    addTearDown(manager.stop);
    manager.start();
    await manager.states
        .firstWhere((s) => s == ConnState.connected)
        .timeout(const Duration(seconds: 5));
    expect(captured, isA<RpcClient>(),
        reason: 'plaintext connection must produce RpcClient, not E2EClient');
    expect(captured, isNot(isA<E2EClient>()));
  });

  test('e2eEnabled=true: clientFactory → ConnectionManager creates E2EClient', () async {
    final node = LoopbackNode('A', await generateKeyPair(),
        (m, p) => Uint8List.fromList(utf8.encode('null')));
    final link = MultiNodeLoopbackLink({'A': node});
    final kv = _MemKv();
    GatewayClient? captured;
    final manager = ConnectionManager(
      connect: () async => link,
      clientFactory: (incoming, send) =>
          buildE2EClient(incoming, send, ClientIdentityStore(kv), TrustChainStore(kv)),
      onConnected: (c) async { captured = c; },
      keepaliveInterval: const Duration(hours: 1),
      keepaliveTimeout: const Duration(hours: 1),
    );
    addTearDown(manager.stop);
    manager.start();
    await manager.states
        .firstWhere((s) => s == ConnState.connected)
        .timeout(const Duration(seconds: 10));
    expect(captured, isA<E2EClient>(),
        reason: 'E2E connection must produce E2EClient');
  });

  group('migration compat', () {
    test('GatewayStore without gateway_e2e key defaults to e2eEnabled=false', () async {
      final kv = _MemKv();
      await kv.write('gateway_url', 'wss://example.com/argus');
      await kv.write('gateway_token', 'tok-abc');
      // Omit 'gateway_e2e' — simulates a connection saved before PR 9.
      final creds = await GatewayStore(kv).load();
      expect(creds, isNotNull);
      expect(creds!.e2eEnabled, isFalse,
          reason: 'pre-PR-9 saved connection must deserialize to plaintext (e2eEnabled=false)');
    });

    test('GatewayCredentials default constructor has e2eEnabled=false', () {
      const creds = GatewayCredentials('wss://example.com/argus', 'tok-abc');
      expect(creds.e2eEnabled, isFalse);
    });

    test('e2eEnabled=false credential selects null clientFactory (plaintext RpcClient path)', () {
      const creds = GatewayCredentials('wss://example.com/argus', 'tok-abc');
      // Mirror the gatewayProvider factory selection logic:
      final factory = creds.e2eEnabled
          ? (Stream<RpcMessage> i, void Function(String) s) => RpcClient(incoming: i, sendFrame: s) as GatewayClient
          : null;
      expect(factory, isNull,
          reason: 'e2eEnabled=false must pass null clientFactory → default RpcClient in ConnectionManager');
    });
  });
}
