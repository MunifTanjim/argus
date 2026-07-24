import 'dart:typed_data';

import 'package:cryptography_plus/cryptography_plus.dart' hide KeyPair;
import 'package:cryptography_plus/dart.dart';

import 'keypair.dart';
import 'session.dart';
import 'symmetric_state.dart';

/// The single fixed Noise protocol name for all argus E2E channels.
const String noiseProtocolName = 'Noise_IK_25519_ChaChaPoly_BLAKE2s';

final DartX25519 _dh = const DartX25519();

// X25519(local.private, remotePublic) -> 32-byte shared secret. Clamping is
// applied internally, matching Go's flynn/noise DH25519.
Uint8List _dhSync(KeyPair local, List<int> remotePublic) {
  final sk = _dh.sharedSecretSync(
    keyPairData: SimpleKeyPairData(
      local.privateKey,
      publicKey: SimplePublicKey(local.publicKey, type: KeyPairType.x25519),
      type: KeyPairType.x25519,
    ),
    remotePublicKey: SimplePublicKey(remotePublic, type: KeyPairType.x25519),
  );
  return Uint8List.fromList((sk as SecretKeyData).bytes);
}

/// A Noise IK handshake. The client is always the initiator; the responder path
/// is provided for tests and completeness.
class HandshakeState {
  HandshakeState._(this._static, this._ss);

  final KeyPair _static;
  final SymmetricState _ss;
  late KeyPair _e;

  /// Initiator: builds msg1 (`e, es, s, ss` + empty payload). Pass [ephemeral]
  /// to pin the ephemeral (tests); production omits it.
  static Future<(HandshakeState, Uint8List)> initiate({
    required KeyPair staticKey,
    required List<int> remoteStatic,
    required List<int> prologue,
    KeyPair? ephemeral,
  }) async {
    final hs = HandshakeState._(staticKey, SymmetricState(noiseProtocolName));
    hs._ss.mixHash(prologue);
    hs._ss.mixHash(remoteStatic); // pre-message: <- s
    hs._e = ephemeral ?? await generateKeyPair();

    final out = BytesBuilder();
    hs._ss.mixHash(hs._e.publicKey); // e
    out.add(hs._e.publicKey);
    hs._ss.mixKey(_dhSync(hs._e, remoteStatic)); // es
    out.add(hs._ss.encryptAndHash(staticKey.publicKey)); // s
    hs._ss.mixKey(_dhSync(staticKey, remoteStatic)); // ss
    out.add(hs._ss.encryptAndHash(const <int>[])); // payload
    return (hs, out.toBytes());
  }

  /// Initiator: consumes msg2 (`e, ee, se` + payload) and returns the session.
  Session finish(List<int> msg2) {
    if (msg2.length < 32) {
      throw const FormatException('e2e: handshake msg2 too short');
    }
    final re = msg2.sublist(0, 32);
    _ss.mixHash(re); // e
    _ss.mixKey(_dhSync(_e, re)); // ee
    _ss.mixKey(_dhSync(_static, re)); // se
    _ss.decryptAndHash(msg2.sublist(32)); // payload (empty)
    final (c1, c2) = _ss.split();
    return Session(enc: c1, dec: c2); // initiator: enc=c1, dec=c2
  }

  /// Responder: consumes msg1, returns (session, authenticated initiator static
  /// public key, msg2). Pass [ephemeral] to pin the ephemeral (tests).
  static Future<(Session, Uint8List, Uint8List)> respond({
    required KeyPair staticKey,
    required List<int> prologue,
    required List<int> msg1,
    KeyPair? ephemeral,
  }) async {
    final ss = SymmetricState(noiseProtocolName);
    ss.mixHash(prologue);
    ss.mixHash(staticKey.publicKey); // pre-message: <- s (own static)

    var off = 0;
    final re = msg1.sublist(off, off + 32);
    off += 32;
    ss.mixHash(re); // e
    ss.mixKey(_dhSync(staticKey, re)); // es
    final encStatic = msg1.sublist(off, off + 48); // 32 + 16 tag
    off += 48;
    final rs = ss.decryptAndHash(encStatic); // initiator static
    ss.mixKey(_dhSync(staticKey, rs)); // ss
    ss.decryptAndHash(msg1.sublist(off)); // payload (empty)

    final e = ephemeral ?? await generateKeyPair();
    final out = BytesBuilder();
    ss.mixHash(e.publicKey); // e
    out.add(e.publicKey);
    ss.mixKey(_dhSync(e, re)); // ee
    ss.mixKey(_dhSync(e, rs)); // se
    out.add(ss.encryptAndHash(const <int>[])); // payload

    final (c1, c2) = ss.split();
    return (Session(enc: c2, dec: c1), Uint8List.fromList(rs), out.toBytes());
  }
}
