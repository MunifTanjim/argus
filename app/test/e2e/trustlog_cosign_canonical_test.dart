import 'dart:typed_data';
import 'package:cryptography_plus/cryptography_plus.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

// I1 parity: a co-signed revoke entry's chain hash must be independent of the
// order the co-signs appear in, matching Go trustlog.hashEntry (which sorts
// co-signs by Signer, then Sig). Without this a gateway could reorder co-signs
// to make the Dart client compute a different Tip than the node.
void main() {
  test('hashEntry is independent of co-sign order (canonical parity with Go)', () async {
    final ed = Ed25519();
    final kpA = await ed.newKeyPair();
    final pubA = Uint8List.fromList((await kpA.extractPublicKey()).bytes);
    final kpB = await ed.newKeyPair();
    final pubB = Uint8List.fromList((await kpB.extractPublicKey()).bytes);

    final prev = Uint8List.fromList(List.filled(32, 1));
    final revoked = [Uint8List.fromList(List.filled(32, 9))];
    final replaces = [Uint8List.fromList(List.filled(32, 10))];

    final template = Entry(kind: Kind.revokeSigner, prev: prev, signers: revoked, replaces: replaces);
    final sb = sigBytes(template);
    final sigA = await ed.sign(sb, keyPair: kpA);
    final sigB = await ed.sign(sb, keyPair: kpB);
    final csA = CoSign(signer: pubA, sig: Uint8List.fromList(sigA.bytes));
    final csB = CoSign(signer: pubB, sig: Uint8List.fromList(sigB.bytes));

    Entry withCoSigns(List<CoSign> cs) => Entry(
          kind: Kind.revokeSigner, prev: prev, signers: revoked, replaces: replaces, coSigns: cs);

    final h1 = hashEntry(withCoSigns([csA, csB]));
    final h2 = hashEntry(withCoSigns([csB, csA])); // same set, reversed order

    expect(h1, equals(h2), reason: 'hashEntry must be independent of co-sign order');
  });
}
