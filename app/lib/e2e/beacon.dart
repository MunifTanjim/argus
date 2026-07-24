import 'dart:convert';
import 'dart:typed_data';

import 'package:cryptography_plus/cryptography_plus.dart';

import 'ed25519.dart' show ed25519Verify;
import 'trustlog/entry.dart' show putField;

final Ed25519 _ed25519 = Ed25519();

/// A signed HEAD announcement emitted by a node. Mirrors Go api.Beacon.
///
/// [beaconPub] is the node's Ed25519 beacon public key (32 bytes); [tip] is
/// the current trust-log tip hash; [length] is the number of entries in the
/// chain; [counter] is a monotonic per-node emission counter (a beacon with a
/// counter ≤ the last seen for that node is stale and ignored); [sig] is an
/// Ed25519 signature over [beaconSigBytes].
class Beacon {
  const Beacon({
    required this.beaconPub,
    required this.tip,
    required this.length,
    required this.counter,
    required this.sig,
  });

  final Uint8List beaconPub;
  final Uint8List tip;
  final int length;
  final int counter;
  final Uint8List sig;

  factory Beacon.fromJson(Map<String, dynamic> json) {
    final beaconPubStr = json['beacon_pub'] as String?;
    final tipStr = json['tip'] as String?;
    final sigStr = json['sig'] as String?;
    return Beacon(
      beaconPub: beaconPubStr != null && beaconPubStr.isNotEmpty
          ? Uint8List.fromList(base64.decode(beaconPubStr))
          : Uint8List(0),
      tip: tipStr != null && tipStr.isNotEmpty
          ? Uint8List.fromList(base64.decode(tipStr))
          : Uint8List(0),
      length: (json['length'] as num).toInt(),
      counter: (json['counter'] as num).toInt(),
      sig: sigStr != null && sigStr.isNotEmpty
          ? Uint8List.fromList(base64.decode(sigStr))
          : Uint8List(0),
    );
  }

  Map<String, dynamic> toJson() => {
        'beacon_pub': base64.encode(beaconPub),
        if (tip.isNotEmpty) 'tip': base64.encode(tip),
        'length': length,
        'counter': counter,
        if (sig.isNotEmpty) 'sig': base64.encode(sig),
      };
}

/// Returns the deterministic byte string a beacon signature covers. Mirrors Go
/// api.beaconSigBytes exactly: [beaconPub] and [tip] are 4-byte big-endian
/// length-prefixed fields; [length] and [counter] are each 8-byte big-endian.
Uint8List beaconSigBytes(Uint8List beaconPub, Uint8List tip, int length, int counter) {
  final b = BytesBuilder();
  putField(b, beaconPub); // 4-byte BE len + bytes
  putField(b, tip); // 4-byte BE len + bytes
  final l8 = Uint8List(8);
  ByteData.sublistView(l8).setUint64(0, length, Endian.big);
  b.add(l8);
  final c8 = Uint8List(8);
  ByteData.sublistView(c8).setUint64(0, counter, Endian.big);
  b.add(c8);
  return b.toBytes();
}

/// Checks that [b.sig] is a valid Ed25519 signature over [beaconSigBytes],
/// using [b.beaconPub] as the verifying key. Mirrors Go api.VerifyBeacon.
/// Returns false if beaconPub is not 32 bytes, sig is empty, or the signature
/// does not verify.
Future<bool> verifyBeacon(Beacon b) async {
  // Total-function verify: a wrong-length beaconPub/sig returns false rather than
  // throwing, matching Go api.VerifyBeacon, so a malformed beacon from an
  // untrusted gateway cannot crash connect().
  final msg = beaconSigBytes(b.beaconPub, b.tip, b.length, b.counter);
  return ed25519Verify(b.beaconPub, msg, b.sig);
}

/// Signs a beacon using the Ed25519 [privateKey] (64-byte seed+pub concatenation
/// as produced by cryptography_plus). Returns a [Beacon] with [sig] set.
/// Mirrors Go api.SignBeacon. [privateKey] must be a valid Ed25519 key pair
/// (seed bytes); [beaconPub] must be the corresponding 32-byte public key.
Future<Beacon> signBeacon(
  SimpleKeyPairData privateKey,
  Uint8List beaconPub,
  Uint8List tip,
  int length,
  int counter,
) async {
  final msg = beaconSigBytes(beaconPub, tip, length, counter);
  final sig = await _ed25519.sign(msg, keyPair: privateKey);
  return Beacon(
    beaconPub: beaconPub,
    tip: tip,
    length: length,
    counter: counter,
    sig: Uint8List.fromList(sig.bytes),
  );
}

/// Generates a fresh Ed25519 key pair for beacon signing. Returns the private
/// key data (for [signBeacon]) and the corresponding 32-byte public key (for
/// [Beacon.beaconPub] and roster advertising).
Future<(SimpleKeyPairData, Uint8List)> generateBeaconKeyPair() async {
  final kp = await _ed25519.newKeyPair();
  final data = await kp.extract();
  final pub = await kp.extractPublicKey();
  return (data, Uint8List.fromList(pub.bytes));
}
