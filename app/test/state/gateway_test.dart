import 'dart:async';
import 'dart:convert';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/transport/rpc_client.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/sessions.dart';

const _s1 =
    '{"id":"mac:%1","tool":"t","status":"working","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"node_label":"mac"}';

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
