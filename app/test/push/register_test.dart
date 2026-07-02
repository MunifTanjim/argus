import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/push/push_provider.dart';
import 'package:argus/push/register.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/transport/rpc_client.dart';

void main() {
  late StreamController<RpcMessage> incoming;
  late List<String> sent;
  late RpcClient client;

  setUp(() {
    incoming = StreamController<RpcMessage>();
    sent = [];
    client = RpcClient(incoming: incoming.stream, sendFrame: sent.add);
  });

  tearDown(() => client.close());

  Map<String, dynamic> frame(String f) =>
      jsonDecode(f.trim()) as Map<String, dynamic>;

  // Respond to the Nth sent frame (0-based) with success or an error, once it
  // has been emitted.
  Future<void> respond(int n, {bool ok = true}) async {
    while (sent.length <= n) {
      await Future<void>.delayed(Duration.zero);
    }
    final id = frame(sent[n])['id'] as String;
    final body = ok
        ? '{"jsonrpc":"2.0","id":"$id","result":null}'
        : '{"jsonrpc":"2.0","id":"$id","error":{"code":-32603,"message":"boom"}}';
    incoming.add(RpcMessage.fromJson(jsonDecode(body)));
  }

  const target = PushTarget('https://ep.example/x', p256dh: 'pk', auth: 'au');

  test('sends push.register with device id and target params', () async {
    final fut = registerWithRetry(client, 'dev-1', target);
    await respond(0);
    expect(await fut, isTrue);
    final f = frame(sent.single);
    expect(f['method'], 'push.register');
    expect(f['params'], {
      'device_id': 'dev-1',
      'endpoint': 'https://ep.example/x',
      'p256dh': 'pk',
      'auth': 'au',
    });
  });

  test('retries on failure then succeeds', () async {
    final fut = registerWithRetry(client, 'dev-1', target,
        attempts: 3, delay: Duration.zero);
    await respond(0, ok: false);
    await respond(1, ok: false);
    await respond(2, ok: true);
    expect(await fut, isTrue);
    expect(sent, hasLength(3));
  });

  test('returns false after exhausting attempts', () async {
    final fut = registerWithRetry(client, 'dev-1', target,
        attempts: 2, delay: Duration.zero);
    await respond(0, ok: false);
    await respond(1, ok: false);
    expect(await fut, isFalse);
    expect(sent, hasLength(2));
  });

  test('unregisterFromGateway sends push.unregister with the device id', () async {
    final fut = unregisterFromGateway(client, 'dev-1');
    await respond(0);
    await fut;
    final f = frame(sent.single);
    expect(f['method'], 'push.unregister');
    expect(f['params'], {'device_id': 'dev-1'});
  });

  test('unregisterFromGateway swallows an RPC error', () async {
    final fut = unregisterFromGateway(client, 'dev-1');
    await respond(0, ok: false);
    await expectLater(fut, completes); // best-effort: does not throw
  });
}
