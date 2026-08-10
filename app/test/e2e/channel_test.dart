import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/transport/jsonrpc.dart' show RpcMessage;

Map<String, dynamic> _vectors() => jsonDecode(
    File('test/e2e/testdata/vectors.json').readAsStringSync()) as Map<String, dynamic>;

void main() {
  test('channelPrologue matches the argus-e2e/v1 format', () {
    expect(channelPrologue('n1', 'c1'), equals(Uint8List.fromList(utf8.encode('argus-e2e/v1|n1|c1'))));
  });

  test('marshalHandshakeFrame round-trips via parse + handshakeFromFrame', () {
    final hs = Uint8List.fromList(List<int>.generate(48, (i) => i));
    final frame = marshalHandshakeFrame('c1', hs);
    final f = RelayFrame.parse(frame);
    expect(f.method, methodE2EHandshake);
    expect(f.route?.chanId, 'c1');
    expect(handshakeFromFrame(f), equals(hs));
  });

  test('parses the Go handshake frame vector', () {
    final v = _vectors()['frame_handshake'] as Map<String, dynamic>;
    final f = RelayFrame.parse(base64.decode(v['frame'] as String));
    expect(f.method, methodE2EHandshake);
    expect(f.route?.chanId, v['chan_id']);
    expect(handshakeFromFrame(f), equals(Uint8List.fromList(base64.decode(v['handshake'] as String))));
  });

  test('parses Go inbound response and notification frames (route/method/id/body)', () {
    final inbound = _vectors()['frame_inbound'] as List;
    final resp = RelayFrame.parse(base64.decode((inbound[0] as Map)['frame'] as String));
    expect(resp.method, isNull);
    expect(resp.id, 42);
    expect(resp.route?.chanId, 'c-frame');
    expect(resp.body, isA<String>());

    final notif = RelayFrame.parse(base64.decode((inbound[1] as Map)['frame'] as String));
    expect(notif.method, 'session.event');
    expect(notif.id, isNull);
    expect(notif.route?.chanId, 'c-frame');
  });

  // Derives the initiator Session from the F1 handshake vectors (fixed ephemeral),
  // matching the fresh session the Go frame vectors were generated from (nonce 0).
  Future<Session> _initiatorSession(Map<String, dynamic> v) async {
    final initStatic = await keyPairFromSeed(base64.decode((v['init_static'] as Map)['priv'] as String));
    final initEph = await keyPairFromSeed(base64.decode((v['init_ephemeral'] as Map)['priv'] as String));
    final respPub = base64.decode((v['resp_static'] as Map)['pub'] as String);
    final prologue = base64.decode(v['prologue'] as String);
    final (hs, _) = await HandshakeState.initiate(
      staticKey: initStatic, remoteStatic: respPub, prologue: prologue, ephemeral: initEph);
    return hs.finish(base64.decode(v['msg2'] as String));
  }

  Future<Session> _responderSession(Map<String, dynamic> v) async {
    final respStatic = await keyPairFromSeed(base64.decode((v['resp_static'] as Map)['priv'] as String));
    final respEph = await keyPairFromSeed(base64.decode((v['resp_ephemeral'] as Map)['priv'] as String));
    final prologue = base64.decode(v['prologue'] as String);
    final (sess, _, __) = await HandshakeState.respond(
      staticKey: respStatic, prologue: prologue, msg1: base64.decode(v['msg1'] as String), ephemeral: respEph);
    return sess;
  }

  test('sealRequestFrame body matches the Go vector and frame is well-formed', () async {
    final v = _vectors();
    final fr = v['frame_request'] as Map<String, dynamic>;
    final ch = Channel(fr['chan_id'] as String, await _initiatorSession(v));
    final params = base64.decode(fr['params'] as String);
    final frameBytes = ch.sealRequestFrame(
        fr['id'] as int, fr['method'] as String, fr['node_id'] as String, params);

    final j = jsonDecode(utf8.decode(frameBytes)) as Map<String, dynamic>;
    expect(j['jsonrpc'], '2.0');
    expect(j['id'], fr['id']);
    expect(j['method'], fr['method']);
    expect((j['route'] as Map)['chan_id'], fr['chan_id']);
    expect((j['route'] as Map)['node_id'], fr['node_id']);
    expect(j.containsKey('params'), isFalse); // no cleartext params
    expect(j['body'], (v['frame_request'] as Map)['body']); // body base64 == Go vector
  });

  test('openResponse and openParams decrypt Go inbound frames in order', () async {
    final v = _vectors();
    final ch = Channel('c-frame', await _initiatorSession(v));
    final inbound = v['frame_inbound'] as List;

    final resp = ch.openResponse(RelayFrame.parse(base64.decode((inbound[0] as Map)['frame'] as String)));
    expect(resp.error, isNull);
    expect(resp.result, equals(Uint8List.fromList(base64.decode((inbound[0] as Map)['result'] as String))));

    final params = ch.openParams(RelayFrame.parse(base64.decode((inbound[1] as Map)['frame'] as String)));
    expect(jsonDecode(utf8.decode(params)),
        equals(jsonDecode(utf8.decode(base64.decode((inbound[1] as Map)['params'] as String)))));
  });

  test('RelayFrame.fromMessage carries route/body from an RpcMessage', () {
    final v = _vectors();
    final line = base64.decode((v['frame_inbound'] as List)[1]['frame'] as String);
    final m = RpcMessage.fromJson(jsonDecode(utf8.decode(line)) as Map<String, dynamic>);
    final f = RelayFrame.fromMessage(m);
    expect(f.method, 'session.event');
    expect(f.route?.chanId, 'c-frame');
    expect(f.body, isA<String>());
  });

  test('openResponse preserves a large int result byte-exactly (web-safe)', () async {
    // Seal a response with a >2^53 integer via a loopback-style local session pair.
    final v = _vectors();
    final ch = Channel('c-frame', await _initiatorSession(v));
    // Build the inbound frame the node would send, using the RESPONDER session.
    final resp = await _responderSession(v);
    final inner = utf8.encode('{"result":{"ts":9223372036854775807}}');
    final body = base64.encode(resp.seal(inner));
    final frame = RelayFrame(route: const RouteHeader(chanId: 'c-frame'), body: body, id: 1, raw: Uint8List(0));
    final r = ch.openResponse(frame);
    expect(r.error, isNull);
    expect(utf8.decode(r.result!), '{"ts":9223372036854775807}');
  });

  test('openResponse maps an error inner to RpcError', () async {
    final v = _vectors();
    final ch = Channel('c-frame', await _initiatorSession(v));
    final resp = await _responderSession(v);
    final inner = utf8.encode('{"error":{"code":-32601,"message":"method not found"}}');
    final body = base64.encode(resp.seal(inner));
    final frame = RelayFrame(route: const RouteHeader(chanId: 'c-frame'), body: body, id: 1, raw: Uint8List(0));
    final r = ch.openResponse(frame);
    expect(r.result, isNull);
    expect(r.error?.code, -32601);
  });

  test('openParams throws on a tampered body', () async {
    final v = _vectors();
    final ch = Channel('c-frame', await _initiatorSession(v));
    final inbound = v['frame_inbound'] as List;
    final frame = RelayFrame.parse(base64.decode((inbound[1] as Map)['frame'] as String));
    // Corrupt the base64 body by flipping a middle character to another base64 char.
    final body = frame.body as String;
    final bytes = base64.decode(body);
    bytes[bytes.length ~/ 2] ^= 0xff;
    final tampered = RelayFrame(method: frame.method, id: frame.id, route: frame.route,
        body: base64.encode(bytes), raw: frame.raw);
    expect(() => ch.openParams(tampered), throwsA(anything));
  });
}
