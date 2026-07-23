import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'package:cryptography_plus/cryptography_plus.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

Map<String, dynamic> _tl() =>
    (jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>;
Uint8List _b(Map<String, dynamic> v, String k) => Uint8List.fromList(base64.decode(v[k] as String));

void main() {
  test('ingest verifies the pinned chain and authorizes only device A', () async {
    final v = _tl();
    final store = TrustStore(_b(v, 'genesis_head'));
    expect(await store.ingest(_b(v, 'chain')), isTrue);
    expect(store.deviceAuthorized(_b(v, 'device_a')), isTrue);
    expect(store.deviceAuthorized(_b(v, 'device_b')), isFalse);
    expect(store.disabled, isFalse);
    expect(store.tip, equals(_b(v, 'head')));
  });

  test('ingesting the same chain twice is a no-op (changed=false)', () async {
    final v = _tl();
    final store = TrustStore(_b(v, 'genesis_head'));
    expect(await store.ingest(_b(v, 'chain')), isTrue);
    expect(await store.ingest(_b(v, 'chain')), isFalse);
  });

  test('a disabled chain flips disabled and turns enforcement off', () async {
    final v = _tl();
    final store = TrustStore(_b(v, 'genesis_head'));
    expect(await store.ingest(_b(v, 'disabled_chain')), isTrue);
    expect(store.disabled, isTrue);
    expect(store.tip, equals(_b(v, 'disabled_head')));
  });

  test('wrong-genesis, tampered, and rollback chains are rejected', () async {
    final v = _tl();
    // wrong genesis
    final s1 = TrustStore(_b(v, 'genesis_head'));
    expect(() => s1.ingest(_b(v, 'wrong_genesis_chain')), throwsA(isA<FormatException>()));
    // tampered: flip a byte in the chain body
    final tampered = Uint8List.fromList(_b(v, 'chain'))..[20] ^= 0xff;
    final s2 = TrustStore(_b(v, 'genesis_head'));
    expect(() => s2.ingest(tampered), throwsA(anything));
    // rollback: ingest the 2-entry chain, then a 1-entry (genesis-only) chain is shorter
    // (reuse disabled_chain which is longer, then the shorter original is a rollback)
    final s3 = TrustStore(_b(v, 'genesis_head'));
    expect(await s3.ingest(_b(v, 'disabled_chain')), isTrue); // 3 entries
    expect(() => s3.ingest(_b(v, 'chain')), throwsA(isA<FormatException>())); // 2 < 3 rollback
  });

  test('fork chain (same genesis, diverges at entry 1) is rejected', () async {
    final v = _tl();
    final store = TrustStore(_b(v, 'genesis_head'));
    expect(await store.ingest(_b(v, 'chain')), isTrue);
    expect(() => store.ingest(_b(v, 'fork_chain')), throwsA(isA<FormatException>()));
  });

  test('chainBytes is null before ingest and equals the adopted chain after', () async {
    final v = _tl();
    final store = TrustStore(_b(v, 'genesis_head'));
    expect(store.chainBytes, isNull);
    await store.ingest(_b(v, 'chain'));
    expect(store.chainBytes, equals(_b(v, 'chain')));
  });

  test('tofu store: first ingest adopts + pins; locked flips false->true', () async {
    final v = _tl();
    final store = TrustStore.tofu();
    expect(store.locked, isFalse);
    expect(await store.ingest(_b(v, 'chain')), isTrue);
    expect(store.locked, isTrue);
    expect(store.deviceAuthorized(_b(v, 'device_a')), isTrue);
    expect(store.tip, equals(_b(v, 'head')));
    expect(store.chainBytes, equals(_b(v, 'chain')));
  });

  test('tofu store: after adopt it is pinned — a divergent same-genesis chain is a fork', () async {
    final v = _tl();
    final store = TrustStore.tofu();
    await store.ingest(_b(v, 'chain'));
    // fork_chain shares the genesis but diverges at entry 1 (added in F5).
    expect(() => store.ingest(_b(v, 'fork_chain')), throwsA(isA<FormatException>()));
  });

  test('tofu store: a longer same-genesis extension is adopted after the first', () async {
    final v = _tl();
    final store = TrustStore.tofu();
    await store.ingest(_b(v, 'chain'));            // 2 entries
    expect(await store.ingest(_b(v, 'disabled_chain')), isTrue); // 3 entries, extends
    expect(store.disabled, isTrue);
  });

  test('tofu store: a garbage first chain is rejected and leaves it not-locked', () async {
    final store = TrustStore.tofu();
    expect(() => store.ingest(Uint8List.fromList([0, 0, 0, 1, 0, 0, 0, 2, 9, 9])),
        throwsA(anything));
    expect(store.locked, isFalse);
  });

  test('pinned store is always locked', () async {
    final v = _tl();
    final store = TrustStore(_b(v, 'genesis_head'));
    expect(store.locked, isTrue); // enforces even before ingest (fail-closed)
  });

  test('removing a signer retroactively invalidates devices it authorized', () async {
    final ed = Ed25519();

    Future<Entry> sign(SimpleKeyPair kp, Entry template) async {
      final s = await ed.sign(sigBytes(template), keyPair: kp);
      return Entry(
        kind: template.kind,
        prev: template.prev,
        signers: template.signers,
        disablements: template.disablements,
        key: template.key,
        signer: template.signer,
        sig: Uint8List.fromList(s.bytes),
      );
    }

    final kpA = await ed.newKeyPair();
    final kpB = await ed.newKeyPair();
    final kpDevA = await ed.newKeyPair();
    final kpDevB = await ed.newKeyPair();

    final pubA = Uint8List.fromList((await kpA.extractPublicKey()).bytes);
    final pubB = Uint8List.fromList((await kpB.extractPublicKey()).bytes);
    final pubDevA = Uint8List.fromList((await kpDevA.extractPublicKey()).bytes);
    final pubDevB = Uint8List.fromList((await kpDevB.extractPublicKey()).bytes);

    // Genesis: signers A and B, signed by A
    final genesis = await sign(kpA, Entry(kind: Kind.genesis, signers: [pubA, pubB], signer: pubA));

    // A authorizes devA
    final e1 = await sign(kpA, Entry(kind: Kind.authorizeDevice, prev: hashEntry(genesis), key: pubDevA, signer: pubA));

    // B authorizes devB
    final e2 = await sign(kpB, Entry(kind: Kind.authorizeDevice, prev: hashEntry(e1), key: pubDevB, signer: pubB));

    // Remove A (B remains — not the last signer)
    final e3 = await sign(kpA, Entry(kind: Kind.removeSigner, prev: hashEntry(e2), key: pubA, signer: pubA));

    final log = await TrustLog.load([genesis, e1, e2, e3]);
    expect(log.deviceAuthorized(pubDevA), isFalse); // A removed → devA invalidated
    expect(log.deviceAuthorized(pubDevB), isTrue);  // B still trusted → devB stays
  });

  test('double-authorize (same device, already authorized) is rejected on load', () async {
    final ed = Ed25519();
    final kpA = await ed.newKeyPair();
    final pubA = Uint8List.fromList((await kpA.extractPublicKey()).bytes);
    final kpB = await ed.newKeyPair();
    final pubB = Uint8List.fromList((await kpB.extractPublicKey()).bytes);
    final dev = Uint8List.fromList(List.filled(32, 0xDD));

    Future<Entry> sign(SimpleKeyPair kp, Entry template) async {
      final s = await ed.sign(sigBytes(template), keyPair: kp);
      return Entry(
        kind: template.kind, prev: template.prev, signers: template.signers,
        disablements: template.disablements, key: template.key, signer: template.signer,
        sig: Uint8List.fromList(s.bytes),
      );
    }

    final genesis = await sign(kpA, Entry(kind: Kind.genesis, signers: [pubA, pubB], signer: pubA));
    final e1 = await sign(kpA, Entry(kind: Kind.authorizeDevice, prev: hashEntry(genesis), key: dev, signer: pubA));
    // Second authorize for the same already-authorized device — must be rejected.
    final e2 = await sign(kpB, Entry(kind: Kind.authorizeDevice, prev: hashEntry(e1), key: dev, signer: pubB));

    expect(
      () => TrustLog.load([genesis, e1, e2]),
      throwsA(isA<FormatException>()),
    );
  });

  test('signer-removal golden vector: devA unauthorized, devB authorized (Go↔Dart parity)', () async {
    final raw = jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>;
    final sr = raw['signer_removal'] as Map<String, dynamic>;
    final chain = Uint8List.fromList(base64.decode(sr['chain'] as String));
    final devA = Uint8List.fromList(base64.decode(sr['dev_a'] as String));
    final devB = Uint8List.fromList(base64.decode(sr['dev_b'] as String));

    final entries = unmarshalChain(chain);
    final log = await TrustLog.load(entries);
    expect(log.deviceAuthorized(devA), isFalse,
        reason: 'devA must be unauthorized after its authorizing signer is removed');
    expect(log.deviceAuthorized(devB), isTrue,
        reason: 'devB must remain authorized (its authorizing signer B is still trusted)');
  });
}
