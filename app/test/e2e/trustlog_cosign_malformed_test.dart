import 'dart:typed_data';
import 'package:cryptography_plus/cryptography_plus.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

// Regression coverage for C1: cryptography_plus's Ed25519.verify THROWS on a
// malformed (non-64-byte) signature, where Go's ed25519.Verify returns false.
// The trust-log verifier must be a total function that treats a malformed
// signature as invalid (skip / false) — never propagate an exception — so that
// an untrusted gateway cannot append a junk co-sign to make the Dart client
// reject a chain the Go node accepts (i.e. suppress a revocation).

Future<(SimpleKeyPair, Uint8List)> _makeSK(Ed25519 ed) async {
  final kp = await ed.newKeyPair();
  final pub = Uint8List.fromList((await kp.extractPublicKey()).bytes);
  return (kp, pub);
}

Future<Entry> _signGenesis(Ed25519 ed, SimpleKeyPair kp, List<Uint8List> signers) async {
  final template = Entry(kind: Kind.genesis, signers: signers, signer: signers[0]);
  final sig = await ed.sign(sigBytes(template), keyPair: kp);
  return Entry(
    kind: Kind.genesis,
    signers: signers,
    signer: signers[0],
    sig: Uint8List.fromList(sig.bytes),
  );
}

void main() {
  // A gateway appends one junk co-sign (a KNOWN trusted signer's pubkey with a
  // wrong-length signature) to an otherwise-valid revoke entry. Go's
  // validCoSigns skips it and counts the genuine co-signs → revocation accepted.
  // The Dart client MUST behave identically (not throw, not reject the chain).
  test(
    'malformed_extra_cosign_is_skipped: '
    'revoke chain with a junk co-sign still loads (parity with Go node)',
    () async {
      final ed = Ed25519();
      final (kpA, pubA) = await _makeSK(ed);
      final (kpB, pubB) = await _makeSK(ed);
      final (_, pubC) = await _makeSK(ed); // trusted, does NOT co-sign validly
      final (_, pubD) = await _makeSK(ed); // replacement

      final genesis = await _signGenesis(ed, kpA, [pubA, pubB, pubC]);

      // Genuine revoke of A (replaced by D), co-signed by A (allowRevoked) + B.
      final template = Entry(
        kind: Kind.revokeSigner,
        prev: hashEntry(genesis),
        signers: [pubA],
        replaces: [pubD],
      );
      final sb = sigBytes(template);
      final sigA = await ed.sign(sb, keyPair: kpA);
      final sigB = await ed.sign(sb, keyPair: kpB);

      final revoke = Entry(
        kind: Kind.revokeSigner,
        prev: hashEntry(genesis),
        signers: [pubA],
        replaces: [pubD],
        coSigns: [
          CoSign(signer: pubA, sig: Uint8List.fromList(sigA.bytes)),
          CoSign(signer: pubB, sig: Uint8List.fromList(sigB.bytes)),
          // Junk co-sign: claims trusted signer C with a wrong-length signature.
          CoSign(signer: pubC, sig: Uint8List(10)),
        ],
      );

      final log = await TrustLog.load([genesis, revoke]);
      expect(log.signerTrusted(pubA), isFalse, reason: 'A must be revoked');
      expect(log.signerTrusted(pubB), isTrue, reason: 'B must stay trusted');
      expect(log.signerTrusted(pubC), isTrue, reason: 'C must stay trusted');
      expect(log.signerTrusted(pubD), isTrue, reason: 'D (replacement) must be trusted');
    },
  );

  // verifyBeacon must return false (matching Go api.VerifyBeacon) — not throw —
  // when the signature has a wrong length, so a malformed beacon from an
  // untrusted gateway cannot crash connect().
  test('verifyBeacon returns false on a wrong-length signature (does not throw)', () async {
    final ed = Ed25519();
    final (_, pub) = await _makeSK(ed);
    final b = Beacon(
      beaconPub: pub,
      tip: Uint8List.fromList([1, 2, 3]),
      length: 1,
      counter: 1,
      sig: Uint8List(10), // wrong length (not 64)
    );
    expect(await verifyBeacon(b), isFalse);
  });
}
