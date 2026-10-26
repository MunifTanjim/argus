import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/transport/frame_reader.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:cbor/simple.dart';

RelayFrame _parseCbor(Uint8List framed) => RelayFrame.fromMessage(
  RpcMessage.fromCbor(cbor.decode(framed.sublist(5)) as Map),
);

Uint8List _cborNotification(String chanId, Uint8List sealed) => frameCbor(
  Uint8List.fromList(
    cbor.encode({
      'jsonrpc': '2.0',
      'method': 'terminal.output',
      'route': {'chan_id': chanId},
      'body': sealed,
    }),
  ),
);

void main() {
  test('plain channel round-trips params', () {
    final c = Channel.plain('c1');
    final params = utf8.encode('{"session_id":"s1"}');
    final frame = c.sealRequestFrame(1, 'sessions.tasks', 'node-a', params);
    final f = RelayFrame.parse(frame);
    expect(c.openParams(f), equals(params));
  });

  test('cbor notification round trip on noise and plain channels', () async {
    final params = {
      'term_id': 't1',
      'data': Uint8List.fromList([0, 1, 2, 255]),
    };
    final inner = Uint8List.fromList(cbor.encode(params));

    final plain = Channel.plain('c1');
    final pf = _parseCbor(_cborNotification('c1', inner));
    expect(pf.cbor, isTrue);
    expect(cbor.decode(plain.openParams(pf)), params);

    final client = await generateKeyPair();
    final node = await generateKeyPair();
    final prologue = channelPrologue('A', 'c2');
    final (hs, msg1) = await HandshakeState.initiate(
      staticKey: client,
      remoteStatic: node.publicKey,
      prologue: prologue,
    );
    final (nodeSess, _, msg2) = await HandshakeState.respond(
      staticKey: node,
      prologue: prologue,
      msg1: msg1,
    );
    final noise = Channel.noise('c2', hs.finish(msg2));
    final nf = _parseCbor(_cborNotification('c2', nodeSess.seal(inner)));
    expect(cbor.decode(noise.openParams(nf)), params);
  });

  test('cbor request frame carries the sealed params as bytes', () {
    final c = Channel.plain('c1');
    final params = Uint8List.fromList(cbor.encode({'term_id': 't1'}));
    final framed = c.sealRequestCbor(7, 'terminal.input', 'A', params);
    expect(framed[0], 0x00);
    final env = cbor.decode(framed.sublist(5)) as Map;
    expect(env['id'], 7);
    expect(env['method'], 'terminal.input');
    expect(env['route'], {'chan_id': 'c1', 'node_id': 'A'});
    expect(env['body'], params);
  });

  test('cbor response result converts to json bytes', () {
    final c = Channel.plain('c1');
    Uint8List response(Object? inner) => frameCbor(
      Uint8List.fromList(
        cbor.encode({
          'jsonrpc': '2.0',
          'id': 1,
          'route': {'chan_id': 'c1'},
          'body': Uint8List.fromList(cbor.encode(inner)),
        }),
      ),
    );
    final nul = c.openResponse(_parseCbor(response({'result': null})));
    expect(utf8.decode(nul.result!), 'null');
    final obj = c.openResponse(
      _parseCbor(
        response({
          'result': {
            'a': 1,
            'b': Uint8List.fromList([1, 2]),
          },
        }),
      ),
    );
    expect(jsonDecode(utf8.decode(obj.result!)), {'a': 1, 'b': 'AQI='});
    final err = c.openResponse(
      _parseCbor(
        response({
          'error': {'code': -32000, 'message': 'boom'},
        }),
      ),
    );
    expect(err.error!.code, -32000);
    expect(err.error!.message, 'boom');
  });
}
