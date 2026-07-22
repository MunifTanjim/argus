import 'dart:typed_data';

import 'package:cryptography_plus/cryptography_plus.dart';
import 'package:cryptography_plus/dart.dart';

/// A Noise cipher state: a 32-byte key plus a monotonic 64-bit record counter.
/// Uses ChaCha20-Poly1305 with the Noise nonce layout (4 zero bytes then the
/// little-endian counter) and returns ciphertext with the 16-byte tag appended.
class CipherState {
  CipherState(List<int> key) : _key = SecretKeyData(List<int>.of(key)) {
    if (key.length != 32) {
      throw ArgumentError('cipher key must be 32 bytes');
    }
  }

  static final DartChacha20 _aead = const DartChacha20.poly1305Aead();
  final SecretKeyData _key;
  int _nonce = 0;

  // 12-byte nonce: 4 zero bytes, then the little-endian counter as two 32-bit
  // halves (web-safe; `setUint64` throws on Flutter web).
  static Uint8List _nonceBytes(int n) {
    final b = Uint8List(12);
    final bd = ByteData.sublistView(b);
    bd.setUint32(4, n & 0xffffffff, Endian.little);
    bd.setUint32(8, (n ~/ 0x100000000) & 0xffffffff, Endian.little);
    return b;
  }

  Uint8List encryptWithAd(List<int> ad, List<int> plaintext) {
    final box = _aead.encryptSync(plaintext,
        secretKey: _key, nonce: _nonceBytes(_nonce), aad: ad);
    _nonce++;
    final out = Uint8List(box.cipherText.length + box.mac.bytes.length)
      ..setRange(0, box.cipherText.length, box.cipherText)
      ..setRange(box.cipherText.length, box.cipherText.length + box.mac.bytes.length,
          box.mac.bytes);
    return out;
  }

  Uint8List decryptWithAd(List<int> ad, List<int> ciphertext) {
    if (ciphertext.length < 16) {
      throw ArgumentError('ciphertext shorter than tag');
    }
    final split = ciphertext.length - 16;
    final box = SecretBox(
      ciphertext.sublist(0, split),
      nonce: _nonceBytes(_nonce),
      mac: Mac(ciphertext.sublist(split)),
    );
    final pt = _aead.decryptSync(box, secretKey: _key, aad: ad);
    _nonce++;
    return Uint8List.fromList(pt);
  }
}
