import 'dart:typed_data';

import 'package:cryptography_plus/cryptography_plus.dart';
import 'package:cryptography_plus/dart.dart';

/// The largest nonce this state will use. Beyond it, incrementing the Dart int
/// counter would overflow on native (2^63) or lose precision on web (doubles are
/// exact only to 2^53), silently reusing a nonce. A real session never approaches
/// this; the guard just turns overflow into a hard error. (Noise reserves the top
/// nonce for rekey, which argus never does.)
const int cipherStateMaxNonce = 0x1FFFFFFFFFFFFF; // 2^53 - 1 (web-safe)

/// A Noise cipher state: a 32-byte key plus a monotonic 64-bit record counter.
/// Uses ChaCha20-Poly1305 with the Noise nonce layout (4 zero bytes then the
/// little-endian counter) and returns ciphertext with the 16-byte tag appended.
class CipherState {
  /// [initialNonce] seeds the record counter; it defaults to 0 and is exposed only
  /// for tests (e.g. to exercise the nonce-exhaustion guard).
  CipherState(List<int> key, {int initialNonce = 0})
      : _key = SecretKeyData(List<int>.of(key)),
        _nonce = initialNonce {
    if (key.length != 32) {
      throw ArgumentError('cipher key must be 32 bytes');
    }
  }

  static final DartChacha20 _aead = const DartChacha20.poly1305Aead();
  final SecretKeyData _key;
  int _nonce;

  void _checkNonce() {
    if (_nonce >= cipherStateMaxNonce) {
      throw StateError('cipher nonce exhausted; refusing to reuse a nonce');
    }
  }

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
    _checkNonce();
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
    _checkNonce();
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
