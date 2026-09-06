import 'dart:typed_data';

import 'package:pointycastle/digests/sha256.dart';

import 'bip39_wordlist.dart';
import 'bytes.dart' show compareBytes;
import 'symmetric_state.dart' show blake2s;

/// BIP39 mnemonic (checksum included) matching Go tyler-smith/go-bip39 word-for-word
/// (same wordlist, same bit order). 32-byte digest → 24 words.
List<String> _bip39Words(Uint8List entropy) {
  final entBits = entropy.length * 8;
  final csBits = entBits ~/ 32; // 8 for 256-bit entropy
  final hash = SHA256Digest().process(entropy);

  bool bitAt(int i) {
    final src = i < entBits ? entropy : hash;
    final j = i < entBits ? i : i - entBits;
    return (src[j >> 3] >> (7 - (j & 7))) & 1 == 1;
  }

  final total = entBits + csBits;
  final words = <String>[];
  for (var i = 0; i < total; i += 11) {
    var idx = 0;
    for (var b = 0; b < 11; b++) {
      idx = (idx << 1) | (bitAt(i + b) ? 1 : 0);
    }
    words.add(bip39English[idx]);
  }
  return words;
}

/// BIP39 fingerprint of the signer set, identical to Go trustlog.SignerSetFingerprint.
List<String> signerSetFingerprintWords(List<Uint8List> signers) {
  final sorted = [...signers]..sort(compareBytes);
  final buf = BytesBuilder();
  final len = Uint8List(4);
  for (final s in sorted) {
    ByteData.sublistView(len).setUint32(0, s.length, Endian.big);
    buf..add(len)..add(s);
  }
  final digest = blake2s(buf.toBytes());
  return _bip39Words(Uint8List.fromList(digest));
}
