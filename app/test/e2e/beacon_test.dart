import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

Map<String, dynamic> _vectors() =>
    jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>;

void main() {
  group('verifyBeacon — golden vector (Go↔Dart parity)', () {
    test('returns true for the valid beacon from vectors.json', () async {
      final v = _vectors()['beacon'] as Map<String, dynamic>;
      final valid = Beacon.fromJson(v['valid'] as Map<String, dynamic>);
      expect(await verifyBeacon(valid), isTrue,
          reason: 'golden valid beacon must verify (matches Go VerifyBeacon)');
    });

    test('returns false for the tampered beacon from vectors.json', () async {
      final v = _vectors()['beacon'] as Map<String, dynamic>;
      final tampered = Beacon.fromJson(v['tampered'] as Map<String, dynamic>);
      expect(await verifyBeacon(tampered), isFalse,
          reason: 'golden tampered beacon (wrong tip, same sig) must not verify');
    });
  });

  group('verifyBeacon — guard checks', () {
    test('returns false when beaconPub is not 32 bytes', () async {
      final b = Beacon(
        beaconPub: Uint8List(16), // wrong length
        tip: Uint8List(32),
        length: 1,
        counter: 1,
        sig: Uint8List(64),
      );
      expect(await verifyBeacon(b), isFalse);
    });

    test('returns false when sig is empty', () async {
      final b = Beacon(
        beaconPub: Uint8List(32),
        tip: Uint8List(32),
        length: 1,
        counter: 1,
        sig: Uint8List(0),
      );
      expect(await verifyBeacon(b), isFalse);
    });
  });

  group('beaconSigBytes — byte layout matches Go', () {
    test('encoding of valid beacon matches Go beaconSigBytes', () {
      final v = _vectors()['beacon'] as Map<String, dynamic>;
      final validJson = v['valid'] as Map<String, dynamic>;
      final b = Beacon.fromJson(validJson);
      // Reconstruct the message that should have been signed:
      // [4-byte BE pub length][pub bytes][4-byte BE tip length][tip bytes][8-byte BE length][8-byte BE counter]
      final msg = beaconSigBytes(b.beaconPub, b.tip, b.length, b.counter);

      // 4-byte len-prefix of pub (32)
      expect(msg[0], 0);
      expect(msg[1], 0);
      expect(msg[2], 0);
      expect(msg[3], 32);
      // pub bytes at offset 4..35
      expect(msg.sublist(4, 36), equals(b.beaconPub));
      // 4-byte len-prefix of tip at offset 36
      expect(msg[36], 0);
      expect(msg[37], 0);
      expect(msg[38], 0);
      expect(msg[39], 32);
      // tip bytes at offset 40..71
      expect(msg.sublist(40, 72), equals(b.tip));
      // 8-byte length at offset 72 (value = 7)
      expect(msg.sublist(72, 80), equals([0, 0, 0, 0, 0, 0, 0, 7]));
      // 8-byte counter at offset 80 (value = 3)
      expect(msg.sublist(80, 88), equals([0, 0, 0, 0, 0, 0, 0, 3]));
      expect(msg.length, 88);
    });
  });

  group('Beacon JSON round-trip', () {
    test('fromJson/toJson round-trips correctly', () {
      final v = _vectors()['beacon'] as Map<String, dynamic>;
      final validJson = v['valid'] as Map<String, dynamic>;
      final b = Beacon.fromJson(validJson);
      final roundTripped = Beacon.fromJson(b.toJson());
      expect(roundTripped.beaconPub, equals(b.beaconPub));
      expect(roundTripped.tip, equals(b.tip));
      expect(roundTripped.length, equals(b.length));
      expect(roundTripped.counter, equals(b.counter));
      expect(roundTripped.sig, equals(b.sig));
    });

    test('tampered beacon differs from valid in tip only', () {
      final v = _vectors()['beacon'] as Map<String, dynamic>;
      final valid = Beacon.fromJson(v['valid'] as Map<String, dynamic>);
      final tampered = Beacon.fromJson(v['tampered'] as Map<String, dynamic>);
      // same sig, pub, length, counter
      expect(tampered.sig, equals(valid.sig));
      expect(tampered.beaconPub, equals(valid.beaconPub));
      expect(tampered.length, equals(valid.length));
      expect(tampered.counter, equals(valid.counter));
      // different tip
      expect(tampered.tip, isNot(equals(valid.tip)));
    });
  });
}
