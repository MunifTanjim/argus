import 'dart:async';
import 'dart:convert';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/transport/rpc_client.dart';
import 'package:argus/state/transcript_controller.dart';

const _chunk = '{"id":"c1","kind":"user","text":"hi"}';

void main() {
  test('subscribeTranscript seeds the store from the catch-up delta', () async {
    final incoming = StreamController<RpcMessage>();
    final sent = <String>[];
    final client = RpcClient(incoming: incoming.stream, sendFrame: sent.add);
    addTearDown(client.close);

    final container = ProviderContainer();
    addTearDown(container.dispose);
    final store = container.read(transcriptProvider('sess').notifier);

    final fut = subscribeTranscript(client, store, sessionId: 'sess');
    final req = jsonDecode(sent.single.trim()) as Map;
    expect(req['method'], 'transcript.subscribe');
    expect((req['params'] as Map)['session_id'], 'sess');
    final id = req['id'] as String;
    final sub = (req['params'] as Map)['sub_id'] as String;

    incoming.add(RpcMessage.fromJson(jsonDecode(
        '{"jsonrpc":"2.0","id":"$id","result":{"sub_id":"$sub","from_index":0,"chunks":[$_chunk]}}')));
    await fut;

    expect(container.read(transcriptProvider('sess')).chunks.single.id, 'c1');
  });

  test('dispatchDelta applies a matching push, ignores other methods', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final store = container.read(transcriptProvider('sess').notifier);
    store.setSubId('sub1');

    dispatchDelta(
        RpcMessage.fromJson(jsonDecode(
            '{"jsonrpc":"2.0","method":"transcript.delta","params":{"sub_id":"sub1","from_index":0,"chunks":[$_chunk]}}')),
        store);
    expect(container.read(transcriptProvider('sess')).chunks.length, 1);

    dispatchDelta(
        RpcMessage.fromJson(jsonDecode(
            '{"jsonrpc":"2.0","method":"session.event","params":{}}')),
        store);
    expect(container.read(transcriptProvider('sess')).chunks.length, 1);
  });
}
