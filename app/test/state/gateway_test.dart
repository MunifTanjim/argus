import 'dart:async';
import 'dart:convert';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/transport/gateway_client.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/transport/rpc_client.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/sessions.dart';

const _s1 =
    '{"id":"mac:%1","agent":"t","status":"working","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"node_label":"mac"}';

/// Minimal [ConnectionManager] subclass with an injectable [client] so tests can
/// control what [equivocationOf] and [startEquivPoll] see without needing a live
/// connection.
class _FakeManager extends ConnectionManager {
  _FakeManager() : super(connect: () async => throw StateError('not used'));

  GatewayClient? _fakeClient;

  @override
  GatewayClient? get client => _fakeClient;
}

/// [E2EClient] subclass with a settable [equivocation] flag. The super-constructor
/// receives a broadcast stream and a no-op sender so no I/O takes place; all
/// E2EClient internal subscriptions are set up normally and torn down by [close].
class _FakeE2EClient extends E2EClient {
  _FakeE2EClient(KeyPair kp)
      : super(
          StreamController<RpcMessage>.broadcast().stream,
          (_) {},
          kp,
        );

  bool _eq = false;

  @override
  bool get equivocation => _eq;
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
    incoming.add(RpcMessage.fromJson(
        jsonDecode('{"jsonrpc":"2.0","id":"$id","result":[$_s1]}')));
    await fut;

    expect(container.read(sessionsProvider).containsKey('mac:%1'), isTrue);
  });

  test(
      'equivocationOf: null→false, non-E2EClient→false, E2EClient follows its flag',
      () async {
    final kp = await generateKeyPair();
    final fc = _FakeE2EClient(kp);
    addTearDown(fc.close);

    expect(equivocationOf(null), isFalse, reason: 'null client → false');

    // RpcClient implements GatewayClient but is not an E2EClient.
    final rc = RpcClient(
        incoming: StreamController<RpcMessage>.broadcast().stream,
        sendFrame: (_) {});
    addTearDown(rc.close);
    expect(equivocationOf(rc), isFalse, reason: 'non-E2EClient → false');

    fc._eq = false;
    expect(equivocationOf(fc), isFalse,
        reason: 'E2EClient with equivocation=false → false');

    fc._eq = true;
    expect(equivocationOf(fc), isTrue,
        reason: 'E2EClient with equivocation=true → true');
  });

  test(
      'startEquivPoll: unconditionally clears stale-true on reconnect and re-arms '
      '(regression: no !equivocation.state guard)', () async {
    // The pre-fix timer body had `if (!equivocation.state)` around the write.
    // After a transient reconnect the new E2EClient starts with equivocation=false,
    // but onDispose only fires on credential change — so the stale true was never
    // cleared, and a subsequent re-detection was silently skipped.
    // The fix (and this test) verifies that every poll tick writes unconditionally.
    // If the guard were reintroduced, equivNotifier.state stays true after the
    // tick where fc._eq==false, and the first expect below would fail.
    final kp = await generateKeyPair();
    final fc = _FakeE2EClient(kp);
    addTearDown(fc.close);
    final manager = _FakeManager();

    final container = ProviderContainer();
    addTearDown(container.dispose);
    final equivNotifier = container.read(equivocationProvider.notifier);

    // Stale-true: left by the previous session before a transient reconnect.
    equivNotifier.state = true;
    // New E2EClient on reconnect starts clean.
    manager._fakeClient = fc;
    fc._eq = false;

    final poll = startEquivPoll(
      manager,
      equivNotifier,
      interval: const Duration(milliseconds: 10),
    );
    addTearDown(poll.cancel);

    // Allow at least one tick.
    await Future<void>.delayed(const Duration(milliseconds: 50));
    expect(container.read(equivocationProvider), isFalse,
        reason: 'unconditional write must clear stale-true');

    // Re-arm: client subsequently detects equivocation.
    fc._eq = true;
    await Future<void>.delayed(const Duration(milliseconds: 50));
    expect(container.read(equivocationProvider), isTrue,
        reason: 'poll must re-arm when client.equivocation becomes true');
  });

  test('dispatchEvent applies session.event, ignores others', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final store = container.read(sessionsProvider.notifier);

    dispatchEvent(
        RpcMessage.fromJson(jsonDecode(
            '{"jsonrpc":"2.0","method":"session.event","params":{"type":"added","session":$_s1}}')),
        store);
    expect(container.read(sessionsProvider).containsKey('mac:%1'), isTrue);

    dispatchEvent(
        RpcMessage.fromJson(jsonDecode(
            '{"jsonrpc":"2.0","method":"transcript.delta","params":{}}')),
        store);
    expect(container.read(sessionsProvider).length, 1);
  });
}
