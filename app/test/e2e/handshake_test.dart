import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

Map<String, dynamic> _vectors() => jsonDecode(
    File('test/e2e/testdata/vectors.json').readAsStringSync()) as Map<String, dynamic>;

Future<KeyPair> _kp(Map<String, dynamic> m) async =>
    keyPairFromSeed(base64.decode(m['priv'] as String));

void main() {
  test('initiator msg1 matches the Go vector byte-for-byte', () async {
    final v = _vectors();
    final initStatic = await _kp(v['init_static'] as Map<String, dynamic>);
    final initEph = await _kp(v['init_ephemeral'] as Map<String, dynamic>);
    final respPub = base64.decode((v['resp_static'] as Map)['pub'] as String);
    final prologue = base64.decode(v['prologue'] as String);

    final (_, msg1) = await HandshakeState.initiate(
      staticKey: initStatic,
      remoteStatic: respPub,
      prologue: prologue,
      ephemeral: initEph,
    );
    expect(msg1, equals(Uint8List.fromList(base64.decode(v['msg1'] as String))));
  });

  test('finish(vector.msg2) yields a session that reproduces the Go seal/open vectors',
      () async {
    final v = _vectors();
    final initStatic = await _kp(v['init_static'] as Map<String, dynamic>);
    final initEph = await _kp(v['init_ephemeral'] as Map<String, dynamic>);
    final respPub = base64.decode((v['resp_static'] as Map)['pub'] as String);
    final prologue = base64.decode(v['prologue'] as String);

    final (hs, _) = await HandshakeState.initiate(
      staticKey: initStatic, remoteStatic: respPub, prologue: prologue, ephemeral: initEph);
    final sess = hs.finish(base64.decode(v['msg2'] as String));

    // enc direction: our seal must equal Go's initiator-seal samples.
    for (final s in v['seal_samples'] as List) {
      final pt = base64.decode((s as Map)['plaintext'] as String);
      expect(sess.seal(pt), equals(Uint8List.fromList(base64.decode(s['sealed'] as String))));
    }
    // dec direction: we must open Go's responder-seal samples.
    for (final s in v['open_samples'] as List) {
      final pt = base64.decode((s as Map)['plaintext'] as String);
      expect(sess.open(base64.decode(s['sealed'] as String)),
          equals(Uint8List.fromList(pt)));
    }
  });

  test('respond(vector.msg1) recovers the initiator static and completes with finish',
      () async {
    final v = _vectors();
    final respStatic = await _kp(v['resp_static'] as Map<String, dynamic>);
    final respEph = await _kp(v['resp_ephemeral'] as Map<String, dynamic>);
    final prologue = base64.decode(v['prologue'] as String);
    final initExpectedPub = base64.decode((v['init_static'] as Map)['pub'] as String);

    final (respSess, initStatic, msg2) = await HandshakeState.respond(
      staticKey: respStatic,
      prologue: prologue,
      msg1: base64.decode(v['msg1'] as String),
      ephemeral: respEph,
    );
    expect(initStatic, equals(Uint8List.fromList(initExpectedPub)));
    expect(msg2, equals(Uint8List.fromList(base64.decode(v['msg2'] as String))));

    // Full loop: the initiator (fresh) finishes against this msg2 and the two
    // sessions interoperate.
    final initStaticKp = await _kp(v['init_static'] as Map<String, dynamic>);
    final initEph = await _kp(v['init_ephemeral'] as Map<String, dynamic>);
    final respPub = base64.decode((v['resp_static'] as Map)['pub'] as String);
    final (ihs, _) = await HandshakeState.initiate(
      staticKey: initStaticKp, remoteStatic: respPub, prologue: prologue, ephemeral: initEph);
    final initSess = ihs.finish(msg2);
    final probe = utf8.encode('ping');
    expect(respSess.open(initSess.seal(probe)), equals(Uint8List.fromList(probe)));
    expect(initSess.open(respSess.seal(probe)), equals(Uint8List.fromList(probe)));
  });

  test('finish throws FormatException on a truncated msg2', () async {
    final a = await generateKeyPair();
    final b = await generateKeyPair();
    final (hs, _) = await HandshakeState.initiate(
        staticKey: a, remoteStatic: b.publicKey, prologue: utf8.encode('argus-e2e/v1|n|c'));
    expect(() => hs.finish(Uint8List(10)), throwsA(isA<FormatException>()));
  });

  test('full handshake with fresh random keys round-trips both directions', () async {
    final a = await generateKeyPair();
    final b = await generateKeyPair();
    final prologue = utf8.encode('argus-e2e/v1|nX|cY');
    final (ihs, msg1) = await HandshakeState.initiate(
        staticKey: a, remoteStatic: b.publicKey, prologue: prologue);
    final (bSess, aStatic, msg2) = await HandshakeState.respond(
        staticKey: b, prologue: prologue, msg1: msg1);
    final aSess = ihs.finish(msg2);
    expect(aStatic, equals(a.publicKey));
    for (final n in [0, 1, 65519 * 2 + 1]) {
      final pt = List<int>.filled(n, 0x7e);
      expect(bSess.open(aSess.seal(pt)), equals(Uint8List.fromList(pt)));
      expect(aSess.open(bSess.seal(pt)), equals(Uint8List.fromList(pt)));
    }
  });
}
