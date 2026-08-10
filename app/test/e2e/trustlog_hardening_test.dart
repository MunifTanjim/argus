import 'dart:typed_data';
import 'package:cryptography_plus/cryptography_plus.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

// Parity guards mirroring Go trustlog.verify: the Dart client must reject the
// same malformed chains the node does, or it would be more permissive on
// adversarial input (the same class of bug as the ed25519 throw-vs-false gap).

Future<(SimpleKeyPair, Uint8List)> _makeSK(Ed25519 ed) async {
  final kp = await ed.newKeyPair();
  return (kp, Uint8List.fromList((await kp.extractPublicKey()).bytes));
}

Future<Entry> _signGenesis(Ed25519 ed, SimpleKeyPair kp, List<Uint8List> signers) async {
  final tmpl = Entry(kind: Kind.genesis, signers: signers, signer: signers[0]);
  final sig = await ed.sign(sigBytes(tmpl), keyPair: kp);
  return Entry(kind: Kind.genesis, signers: signers, signer: signers[0], sig: Uint8List.fromList(sig.bytes));
}

void main() {
  test('genesis with duplicate signers is rejected', () async {
    final ed = Ed25519();
    final (kpA, pubA) = await _makeSK(ed);
    final genesis = await _signGenesis(ed, kpA, [pubA, pubA]);
    await expectLater(() => TrustLog.load([genesis]), throwsA(isA<FormatException>()));
  });

  test('revoke listing a pubkey in both revoked and replacements is rejected', () async {
    final ed = Ed25519();
    final (kpA, pubA) = await _makeSK(ed);
    final (kpB, pubB) = await _makeSK(ed);
    final genesis = await _signGenesis(ed, kpA, [pubA, pubB]);

    // Revoke B while also listing B as a replacement — contradictory overlap.
    final tmpl = Entry(kind: Kind.revokeSigner, prev: hashEntry(genesis), signers: [pubB], replaces: [pubB]);
    final sb = sigBytes(tmpl);
    final sigA = await ed.sign(sb, keyPair: kpA);
    final sigB = await ed.sign(sb, keyPair: kpB);
    final revoke = Entry(
      kind: Kind.revokeSigner, prev: hashEntry(genesis), signers: [pubB], replaces: [pubB],
      coSigns: [
        CoSign(signer: pubA, sig: Uint8List.fromList(sigA.bytes)),
        CoSign(signer: pubB, sig: Uint8List.fromList(sigB.bytes)),
      ],
    );
    await expectLater(() => TrustLog.load([genesis, revoke]), throwsA(isA<FormatException>()));
  });
}
