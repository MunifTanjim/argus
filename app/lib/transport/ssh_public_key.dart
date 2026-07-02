import 'dart:convert';
import 'dart:typed_data';

/// Encode an RSA public key as an OpenSSH authorized_keys line:
/// `ssh-rsa <base64(wire)> [comment]`.
String rsaOpenSshPublicKey(BigInt modulus, BigInt exponent,
    {String comment = ''}) {
  final buf = BytesBuilder();
  _writeField(buf, ascii.encode('ssh-rsa'));
  _writeField(buf, _mpint(exponent));
  _writeField(buf, _mpint(modulus));
  final b64 = base64.encode(buf.toBytes());
  return comment.isEmpty ? 'ssh-rsa $b64' : 'ssh-rsa $b64 $comment';
}

void _writeField(BytesBuilder buf, List<int> bytes) {
  final len = bytes.length;
  buf.add([
    (len >> 24) & 0xff,
    (len >> 16) & 0xff,
    (len >> 8) & 0xff,
    len & 0xff,
  ]);
  buf.add(bytes);
}

/// Big-endian two's-complement, with a leading 0x00 when the top bit is set.
Uint8List _mpint(BigInt v) {
  var bytes = _bigIntToBytes(v);
  if (bytes.isNotEmpty && (bytes.first & 0x80) != 0) {
    bytes = Uint8List.fromList([0x00, ...bytes]);
  }
  return bytes;
}

Uint8List _bigIntToBytes(BigInt v) {
  // RSA moduli/exponents are always non-negative; this encodes magnitude only,
  // so a negative input would silently produce a wrong (unsigned) result.
  assert(!v.isNegative, 'mpint encoding expects a non-negative value');
  if (v == BigInt.zero) return Uint8List.fromList([0]);
  final out = <int>[];
  var n = v;
  while (n > BigInt.zero) {
    out.add((n & BigInt.from(0xff)).toInt());
    n = n >> 8;
  }
  return Uint8List.fromList(out.reversed.toList());
}
