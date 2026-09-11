import 'dart:convert';
import 'dart:math';
import 'dart:typed_data';

import 'package:cryptography_plus/cryptography_plus.dart';
import 'package:pointycastle/ecc/curves/prime256v1.dart';

final _p256 = ECCurve_prime256v1();

class WebPushKeys {
  final String p256dh;
  final String auth;
  final List<int> privateKey;

  const WebPushKeys({
    required this.p256dh,
    required this.auth,
    required this.privateKey,
  });
}

Future<WebPushKeys> generateWebPushKeys() async {
  final rng = Random.secure();
  final d = Uint8List.fromList(List.generate(32, (_) => rng.nextInt(256)));
  final authBytes = Uint8List.fromList(List.generate(16, (_) => rng.nextInt(256)));
  return WebPushKeys(
    p256dh: _b64url(_p256PublicFromPrivate(d)),
    auth: _b64url(authBytes),
    privateKey: d,
  );
}

Future<List<int>> decryptWebPush({
  required List<int> privateKey,
  required String auth,
  required List<int> body,
}) async {
  if (body.length < 21) throw ArgumentError('body too short');

  final salt = body.sublist(0, 16);
  final idLen = body[20];
  final asPub = body.sublist(21, 21 + idLen);
  final ciphertext = body.sublist(21 + idLen);

  final uaPub = _p256PublicFromPrivate(privateKey);
  final shared = _p256Ecdh(privateKey, asPub);

  final authBytes = base64Url.decode(auth + '=' * ((4 - auth.length % 4) % 4));

  // RFC 8291 §3.4: derive IKM from ECDH secret
  final keyInfo = [
    ...utf8.encode('WebPush: info\x00'),
    ...uaPub,
    ...asPub,
  ];
  final ikmKey = await Hkdf(hmac: Hmac.sha256(), outputLength: 32).deriveKey(
    secretKey: SecretKey(shared),
    nonce: authBytes,
    info: keyInfo,
  );
  final ikm = await ikmKey.extractBytes();

  // RFC 8188: extract PRK from IKM + per-message salt
  final prk = (await Hmac.sha256().calculateMac(ikm, secretKey: SecretKey(salt))).bytes;

  final cek = await _hkdfExpand(prk, utf8.encode('Content-Encoding: aes128gcm\x00'), 16);
  final nonce = await _hkdfExpand(prk, utf8.encode('Content-Encoding: nonce\x00'), 12);

  final secretBox = SecretBox(
    ciphertext.sublist(0, ciphertext.length - 16),
    nonce: nonce,
    mac: Mac(ciphertext.sublist(ciphertext.length - 16)),
  );
  final record = await AesGcm.with128bits().decrypt(secretBox, secretKey: SecretKey(cek));

  // Strip trailing 0x02 last-record delimiter (and any zero padding before it)
  var i = record.length - 1;
  while (i >= 0 && record[i] == 0x00) {
    i--;
  }
  if (i < 0 || record[i] != 0x02) throw StateError('missing padding delimiter');
  return record.sublist(0, i);
}

String _b64url(List<int> bytes) => base64Url.encode(bytes).replaceAll('=', '');

BigInt _bytesToBigInt(List<int> bytes) {
  var result = BigInt.zero;
  for (final b in bytes) {
    result = (result << 8) | BigInt.from(b);
  }
  return result;
}

Uint8List _bigIntTo32Bytes(BigInt n) {
  final result = Uint8List(32);
  var value = n;
  for (var i = 31; i >= 0; i--) {
    result[i] = (value & BigInt.from(0xFF)).toInt();
    value >>= 8;
  }
  return result;
}

Uint8List _p256PublicFromPrivate(List<int> privateKeyBytes) {
  final d = _bytesToBigInt(privateKeyBytes);
  return (_p256.G * d)!.getEncoded(false);
}

Uint8List _p256Ecdh(List<int> privateKeyBytes, List<int> peerPublicBytes) {
  final d = _bytesToBigInt(privateKeyBytes);
  final sharedPoint = (_p256.curve.decodePoint(peerPublicBytes)! * d)!;
  return _bigIntTo32Bytes(sharedPoint.x!.toBigInteger()!);
}

// HKDF-Expand(PRK, info, L) per RFC 5869
Future<Uint8List> _hkdfExpand(List<int> prk, List<int> info, int length) async {
  final hmac = Hmac.sha256();
  final prkKey = SecretKey(prk);
  var t = <int>[];
  final result = <int>[];
  var counter = 1;
  while (result.length < length) {
    t = (await hmac.calculateMac(
      [...t, ...info, counter & 0xFF],
      secretKey: prkKey,
    )).bytes;
    result.addAll(t);
    counter++;
  }
  return Uint8List.fromList(result.sublist(0, length));
}
