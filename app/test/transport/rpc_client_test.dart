import 'dart:async';
import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
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

  String idOf(String frame) =>
      (jsonDecode(frame.trim()) as Map<String, dynamic>)['id'] as String;

  test('call resolves with the matching response result', () async {
    final fut = client.call('sessions.list');
    expect(sent, hasLength(1));
    final id = idOf(sent.single);
    incoming.add(RpcMessage.fromJson(
        jsonDecode('{"jsonrpc":"2.0","id":"$id","result":[{"a":1}]}')));
    expect(await fut, [
      {'a': 1}
    ]);
  });

  test('call throws on an error response', () async {
    final fut = client.call('nope');
    final id = idOf(sent.single);
    incoming.add(RpcMessage.fromJson(jsonDecode(
        '{"jsonrpc":"2.0","id":"$id","error":{"code":-32601,"message":"no method"}}')));
    await expectLater(fut, throwsA(isA<RpcError>()));
  });

  test('notifications stream surfaces server pushes', () async {
    final got = <String>[];
    client.notifications.listen((m) => got.add(m.method!));
    incoming.add(RpcMessage.fromJson(
        jsonDecode('{"jsonrpc":"2.0","method":"session.event","params":{}}')));
    await Future<void>.delayed(Duration.zero);
    expect(got, ['session.event']);
  });

  test('ids are unique per call', () async {
    client.call('ping').ignore();
    client.call('ping').ignore();
    expect(idOf(sent[0]) == idOf(sent[1]), isFalse);
  });
}
