import 'dart:convert';
import 'dart:typed_data';

import 'package:cryptography_plus/dart.dart';

import 'cipher_state.dart';

final DartBlake2s _blake2s = const DartBlake2s();

Uint8List blake2s(List<int> data) =>
    Uint8List.fromList(_blake2s.hashSync(data).bytes);

// Noise spec: BLOCKLEN = 64 for BLAKE2s (compression block size, not output size).
const int _hmacBlockLen = 64;

Uint8List _hmacBlake2s(List<int> key, List<int> data) {
  var k = key.length > _hmacBlockLen ? blake2s(key) : List<int>.of(key);
  if (k.length < _hmacBlockLen) {
    k = [...k, ...List<int>.filled(_hmacBlockLen - k.length, 0)];
  }
  final ipad = List<int>.generate(_hmacBlockLen, (i) => k[i] ^ 0x36);
  final opad = List<int>.generate(_hmacBlockLen, (i) => k[i] ^ 0x5c);
  final inner = blake2s([...ipad, ...data]);
  return blake2s([...opad, ...inner]);
}

/// Noise HKDF (HMAC-BLAKE2s); [numOutputs] must be 2 or 3.
List<Uint8List> hkdf(List<int> chainingKey, List<int> ikm, int numOutputs) {
  final tmp = _hmacBlake2s(chainingKey, ikm);
  final o1 = _hmacBlake2s(tmp, const <int>[0x01]);
  if (numOutputs == 2) {
    return [o1, _hmacBlake2s(tmp, [...o1, 0x02])];
  }
  final o2 = _hmacBlake2s(tmp, [...o1, 0x02]);
  final o3 = _hmacBlake2s(tmp, [...o2, 0x03]);
  return [o1, o2, o3];
}

class SymmetricState {
  SymmetricState(String protocolName)
      : _h = _initH(protocolName),
        _ck = _initH(protocolName);

  Uint8List _h;
  Uint8List _ck;
  CipherState? _cipher;

  static Uint8List _initH(String name) {
    final nameBytes = utf8.encode(name);
    if (nameBytes.length <= 32) {
      final h = Uint8List(32);
      h.setRange(0, nameBytes.length, nameBytes);
      return h;
    }
    return blake2s(nameBytes);
  }

  void mixHash(List<int> data) {
    _h = blake2s([..._h, ...data]);
  }

  void mixKey(List<int> ikm) {
    final out = hkdf(_ck, ikm, 2);
    _ck = out[0];
    _cipher = CipherState(out[1]);
  }

  Uint8List encryptAndHash(List<int> plaintext) {
    final ct = _cipher == null
        ? Uint8List.fromList(plaintext)
        : _cipher!.encryptWithAd(_h, plaintext);
    mixHash(ct);
    return ct;
  }

  Uint8List decryptAndHash(List<int> ciphertext) {
    final pt = _cipher == null
        ? Uint8List.fromList(ciphertext)
        : _cipher!.decryptWithAd(_h, ciphertext);
    mixHash(ciphertext);
    return pt;
  }

  (CipherState, CipherState) split() {
    final out = hkdf(_ck, const <int>[], 2);
    return (CipherState(out[0]), CipherState(out[1]));
  }

  Uint8List get handshakeHash => _h;
}
