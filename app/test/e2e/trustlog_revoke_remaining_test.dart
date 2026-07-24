import 'dart:typed_data';
import 'package:cryptography_plus/cryptography_plus.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

// Helpers -------------------------------------------------------------------

Future<(SimpleKeyPair, Uint8List)> _makeSK(Ed25519 ed) async {
  final kp = await ed.newKeyPair();
  final pub = Uint8List.fromList((await kp.extractPublicKey()).bytes);
  return (kp, pub);
}

/// Builds and signs a genesis entry. kp must correspond to signers[0].
Future<Entry> _signGenesis(
  Ed25519 ed,
  SimpleKeyPair kp,
  List<Uint8List> signers,
) async {
  final template = Entry(kind: Kind.genesis, signers: signers, signer: signers[0]);
  final sig = await ed.sign(sigBytes(template), keyPair: kp);
  return Entry(
    kind: Kind.genesis,
    signers: signers,
    signer: signers[0],
    sig: Uint8List.fromList(sig.bytes),
  );
}

/// Builds a KindRevokeSigner entry co-signed by all of [coSigners].
Future<Entry> _buildRevoke(
  Ed25519 ed,
  Uint8List prev,
  List<Uint8List> revoked,
  List<Uint8List> replaces,
  List<(SimpleKeyPair, Uint8List)> coSigners,
) async {
  final template = Entry(
    kind: Kind.revokeSigner,
    prev: prev,
    signers: revoked,
    replaces: replaces,
  );
  final sb = sigBytes(template);
  final coSigns = <CoSign>[];
  for (final (kp, pub) in coSigners) {
    final sig = await ed.sign(sb, keyPair: kp);
    coSigns.add(CoSign(signer: pub, sig: Uint8List.fromList(sig.bytes)));
  }
  return Entry(
    kind: Kind.revokeSigner,
    prev: prev,
    signers: revoked,
    replaces: replaces,
    coSigns: coSigns,
  );
}

// Tests: _verify remaining-signer count for KindRevokeSigner ----------------
//
// DESIGN NOTE — why the zero-signers guard (remaining < 1) cannot be triggered
// via TrustLog.load:
//
// _verify checks co-signs BEFORE the remaining-count guard.  For remaining=0,
// every signer in (_signers ∪ replaces) must be in the revoked list, so
// |e.signers| ≥ |_signers|.  The maximum valid co-sign count is n = |_signers|
// (only currently-trusted signers contribute even when allowRevoked=true).
// The co-sign check requires n > |e.signers| ≥ |_signers|, but n ≤ |_signers|,
// making the inequality impossible.  The zero-signers guard is therefore a
// defensive backstop; the co-sign check always fires first.
//
// Go's own test (TestRevokeSignerCannotRevokeEntireSignerSet) confirms this:
// "the error is the co-sign check, not the last-signer guard".
//
// The tests below drive _verify's revoke path through TrustLog.load:
//   • reject test  — co-sign gate enforces the boundary
//   • accept tests — remaining-count gate (the succeeding branch of _verify)
//
// Case 3 (dedup flip) is omitted: the flip scenario (dedup gives remaining=0
// while non-dedup gives remaining=1) also requires remaining<1 with co-sign
// passing, which is the same proven impossibility.

void main() {
  // Case 1 — reject: revoke-all without replacement is blocked.
  //
  // Genesis {A, B} signed by A.  Attempt to revoke both A and B with no
  // replacement.  allowRevoked=false (replaces empty), so A and B are excluded
  // from the valid co-signer set; n=0 fails "0 > 2", triggering the co-sign
  // guard before the remaining-count guard is ever reached.
  test(
    'revoke_all_no_replacement_rejected: '
    'co-sign guard blocks all-signer revoke (zero-signers backstop unreachable via load)',
    () async {
      final ed = Ed25519();
      final (kpA, pubA) = await _makeSK(ed);
      final (kpB, pubB) = await _makeSK(ed);

      final genesis = await _signGenesis(ed, kpA, [pubA, pubB]);
      // Both A and B co-sign, but since they are the revoked set and
      // allowRevoked=false, neither counts — n=0 which is not > 2.
      final revoke = await _buildRevoke(
        ed,
        hashEntry(genesis),
        [pubA, pubB],
        [], // no replacement
        [(kpA, pubA), (kpB, pubB)],
      );

      await expectLater(
        () => TrustLog.load([genesis, revoke]),
        throwsA(predicate<FormatException>(
          (e) => e.message == 'trustlog: revoke-signer lacks enough valid co-signs',
          'co-sign guard message',
        )),
      );
    },
  );

  // Case 2 — accept: revoke one signer from {A, B} with replacement C.
  //
  // allowRevoked=true (replaces non-empty) lets revoked A co-sign alongside B;
  // n=2 > 1 (one revoked) passes the co-sign check.
  // withReplaces=3 (A,B from _signers + C new), revoke A → remaining=2 ≥ 1.
  // _verify's remaining-count guard passes; after fold: _signers = {B, C}.
  test(
    'revoke_one_with_replacement_accepted: '
    '_verify remaining-count passes (remaining=2), signer set {B,C}',
    () async {
      final ed = Ed25519();
      final (kpA, pubA) = await _makeSK(ed);
      final (kpB, pubB) = await _makeSK(ed);
      final (_, pubC) = await _makeSK(ed);

      final genesis = await _signGenesis(ed, kpA, [pubA, pubB]);
      final revoke = await _buildRevoke(
        ed,
        hashEntry(genesis),
        [pubA], // revoke A
        [pubC], // replacement C
        [(kpA, pubA), (kpB, pubB)], // A (allowRevoked) + B co-sign
      );

      final log = await TrustLog.load([genesis, revoke]);
      expect(log.signerTrusted(pubA), isFalse, reason: 'A must be revoked');
      expect(log.signerTrusted(pubB), isTrue, reason: 'B must stay trusted');
      expect(log.signerTrusted(pubC), isTrue, reason: 'C (replacement) must be trusted');
    },
  );

  // Accept — revoke two signers from {A, B, C} with replacement D.
  //
  // allowRevoked=true; A, B, C all co-sign → n=3 > 2 (two revoked) passes.
  // withReplaces=4 (A,B,C from _signers + D new), revoke A,B → remaining=2 ≥ 1.
  // _verify's remaining-count guard passes; after fold: _signers = {C, D}.
  test(
    'revoke_two_with_replacement_accepted: '
    '_verify remaining-count passes (remaining=2), signer set {C,D}',
    () async {
      final ed = Ed25519();
      final (kpA, pubA) = await _makeSK(ed);
      final (kpB, pubB) = await _makeSK(ed);
      final (kpC, pubC) = await _makeSK(ed);
      final (_, pubD) = await _makeSK(ed);

      final genesis = await _signGenesis(ed, kpA, [pubA, pubB, pubC]);
      final revoke = await _buildRevoke(
        ed,
        hashEntry(genesis),
        [pubA, pubB], // revoke A and B
        [pubD],       // replacement D
        [(kpA, pubA), (kpB, pubB), (kpC, pubC)], // A+B (allowRevoked) + C co-sign
      );

      final log = await TrustLog.load([genesis, revoke]);
      expect(log.signerTrusted(pubA), isFalse, reason: 'A must be revoked');
      expect(log.signerTrusted(pubB), isFalse, reason: 'B must be revoked');
      expect(log.signerTrusted(pubC), isTrue, reason: 'C must stay trusted');
      expect(log.signerTrusted(pubD), isTrue, reason: 'D (replacement) must be trusted');
    },
  );
}
