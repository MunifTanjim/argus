import 'dart:typed_data';

import 'cipher_state.dart';

/// An established E2E channel: [_enc] encrypts outbound, [_dec] decrypts inbound.
/// Messages MUST be processed in order per direction (implicit Noise nonces).
class Session {
  Session({required CipherState enc, required CipherState dec})
      : _enc = enc,
        _dec = dec;

  final CipherState _enc;
  final CipherState _dec;

  // Largest plaintext per record: 65535 record ceiling minus the 16-byte tag.
  static const int _maxChunk = 65535 - 16;

  static Uint8List _recordAd(int index, bool isFinal) {
    final ad = Uint8List(5);
    ByteData.sublistView(ad).setUint32(0, index, Endian.big);
    ad[4] = isFinal ? 1 : 0;
    return ad;
  }

  /// Encrypts [plaintext] into one or more length-prefixed Noise records.
  Uint8List seal(List<int> plaintext) {
    final out = BytesBuilder();
    var index = 0;
    var offset = 0;
    while (true) {
      final end = (offset + _maxChunk < plaintext.length)
          ? offset + _maxChunk
          : plaintext.length;
      final chunk = plaintext.sublist(offset, end);
      offset = end;
      final isFinal = offset == plaintext.length;
      final ct = _enc.encryptWithAd(_recordAd(index, isFinal), chunk);
      if (ct.length > 0xffff) {
        throw StateError('e2e: record exceeds 65535 bytes');
      }
      final hdr = Uint8List(2);
      ByteData.sublistView(hdr).setUint16(0, ct.length, Endian.big);
      out.add(hdr);
      out.add(ct);
      if (isFinal) return out.toBytes();
      index++;
    }
  }

  /// Decrypts a blob of length-prefixed records back into the application message.
  Uint8List open(List<int> blob) {
    if (blob.isEmpty) {
      throw ArgumentError('e2e: empty blob');
    }
    final data = blob is Uint8List ? blob : Uint8List.fromList(blob);
    final view = ByteData.sublistView(data);
    final out = BytesBuilder();
    var index = 0;
    var offset = 0;
    while (offset < data.length) {
      if (data.length - offset < 2) {
        throw StateError('e2e: truncated record header');
      }
      final n = view.getUint16(offset, Endian.big);
      offset += 2;
      if (data.length - offset < n) {
        throw StateError('e2e: truncated record body');
      }
      final record = data.sublist(offset, offset + n);
      offset += n;
      final isFinal = offset == data.length;
      out.add(_dec.decryptWithAd(_recordAd(index, isFinal), record));
      index++;
    }
    return out.toBytes();
  }
}
