import 'dart:math';
import 'dart:typed_data';

import 'package:cryptography_plus/cryptography_plus.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

// --- helpers ---

class _SK {
  final SimpleKeyPair kp;
  final Uint8List pub;
  _SK(this.kp, this.pub);
}

Future<_SK> _makeSK(Ed25519 ed) async {
  final kp = await ed.newKeyPair();
  final pub = Uint8List.fromList((await kp.extractPublicKey()).bytes);
  return _SK(kp, pub);
}

/// Signs a template entry (template.signer must already be set to by.pub).
Future<Entry> _sign(Ed25519 ed, _SK by, Entry template) async {
  final sig = await ed.sign(sigBytes(template), keyPair: by.kp);
  return Entry(
    kind: template.kind,
    prev: template.prev,
    signers: template.signers,
    disablements: template.disablements,
    key: template.key,
    signer: template.signer,
    sig: Uint8List.fromList(sig.bytes),
    coSigns: template.coSigns,
    replaces: template.replaces,
  );
}

/// Builds a KindRevokeSigner entry co-signed by all of [coSigners].
Future<Entry> _buildRevoke(
  Ed25519 ed,
  Uint8List prev,
  List<Uint8List> revoked,
  List<Uint8List> replaces,
  List<_SK> coSigners,
) async {
  // Template with no signer/sig (revokeSigner uses coSigns; signer and key are null).
  final template = Entry(
    kind: Kind.revokeSigner,
    prev: prev,
    signers: revoked,
    replaces: replaces,
  );
  final sb = sigBytes(template);
  final coSigns = <CoSign>[];
  for (final cs in coSigners) {
    final sig = await ed.sign(sb, keyPair: cs.kp);
    coSigns.add(CoSign(signer: cs.pub, sig: Uint8List.fromList(sig.bytes)));
  }
  return Entry(
    kind: Kind.revokeSigner,
    prev: prev,
    signers: revoked,
    replaces: replaces,
    coSigns: coSigns,
  );
}

/// Generates a deterministic random valid chain: a genesis with 3 signers (so
/// revoke-signer is exercisable) plus [ops] random mutations. The signer key
/// list is kept in sync with the log's real signer set. Mirrors Go genChain.
Future<List<Entry>> _genChain(int seed, int ops) async {
  final r = Random(seed);
  final ed = Ed25519();

  final s1 = await _makeSK(ed);
  final s2 = await _makeSK(ed);
  final s3 = await _makeSK(ed);
  var keys = <_SK>[s1, s2, s3];

  // Genesis signed by s1.
  final genesisTemplate = Entry(
    kind: Kind.genesis,
    signers: [s1.pub, s2.pub, s3.pub],
    signer: s1.pub,
  );
  final genesis = await _sign(ed, s1, genesisTemplate);
  final entries = <Entry>[genesis];
  final devs = <Uint8List>[]; // currently tracked authorized devices

  for (var i = 0; i < ops; i++) {
    final tip = hashEntry(entries.last);
    switch (r.nextInt(5)) {
      case 0: // authorize a fresh device
        final dev = await _makeSK(ed);
        final by = keys[r.nextInt(keys.length)];
        final e = await _sign(ed, by, Entry(
          kind: Kind.authorizeDevice,
          prev: tip,
          key: dev.pub,
          signer: by.pub,
        ));
        entries.add(e);
        devs.add(dev.pub);

      case 1: // revoke a known device
        if (devs.isNotEmpty) {
          // Use identity equality — devs holds the exact same reference.
          final dev = devs[r.nextInt(devs.length)];
          final by = keys[r.nextInt(keys.length)];
          final e = await _sign(ed, by, Entry(
            kind: Kind.revokeDevice,
            prev: tip,
            key: dev,
            signer: by.pub,
          ));
          entries.add(e);
          devs.remove(dev); // identity-equal removal works (same reference)
        }

      case 2: // add a signer
        final ns = await _makeSK(ed);
        final by = keys[r.nextInt(keys.length)];
        final e = await _sign(ed, by, Entry(
          kind: Kind.addSigner,
          prev: tip,
          key: ns.pub,
          signer: by.pub,
        ));
        entries.add(e);
        keys = [...keys, ns];

      case 3: // remove the last signer (never the last remaining one); keep keys in sync
        if (keys.length > 1) {
          final removedKey = keys.last;
          final by = keys.first; // first signer signs the removal
          final e = await _sign(ed, by, Entry(
            kind: Kind.removeSigner,
            prev: tip,
            key: removedKey.pub,
            signer: by.pub,
          ));
          entries.add(e);
          keys = keys.sublist(0, keys.length - 1);
        }

      case 4: // revoke a signer co-signed by all others (need ≥3 so 2 can co-sign 1 revocation)
        if (keys.length >= 3) {
          final revokedIdx = keys.length - 1;
          final revokedKey = keys[revokedIdx];
          final coSigners = keys.sublist(0, revokedIdx); // all except revoked

          // 50% chance to include a replacement signer.
          List<Uint8List> replacePub = [];
          _SK? newKey;
          if (r.nextInt(2) == 0) {
            newKey = await _makeSK(ed);
            replacePub = [newKey.pub];
          }

          final revokeEntry = await _buildRevoke(
              ed, tip, [revokedKey.pub], replacePub, coSigners);
          entries.add(revokeEntry);
          keys = keys.sublist(0, revokedIdx);
          if (newKey != null) {
            keys = [...keys, newKey];
          }
        }
    }
  }

  return entries;
}

// --- reference and fold-only helpers ---

/// Reference: replay the prefix through TrustLog.load (_apply = _verify + _fold)
/// and return the trusted signer set.
Future<Set<String>> _foldViaApply(List<Entry> entries, int p) async {
  if (p == 0) return const {};
  final l = await TrustLog.load(entries.sublist(0, p));
  return l.signerHexSet;
}

/// Fold-only: uses foldSignersAtForTest, which is backed by _foldSignersAt.
/// Before the split: _foldSignersAt uses TrustLog.load (_apply).
/// After the split: _foldSignersAt uses TrustLog.foldOnly (_fold only).
/// Both must return the same signer set for the differential test to pass.
Future<Set<String>> _foldOnly(List<Entry> entries, int p) async {
  return foldSignersAtForTest(entries, p);
}

// --- differential test ---

void main() {
  test('fold-only foldSignersAt matches full-apply reference across forks', () async {
    var totalRevokes = 0;

    for (var seed = 1; seed <= 40; seed++) {
      final entries = await _genChain(seed, 8);
      totalRevokes += entries.where((e) => e.kind == Kind.revokeSigner).length;

      for (var p = 1; p <= entries.length; p++) {
        final want = await _foldViaApply(entries, p);
        final got = await _foldOnly(entries, p);
        expect(got, equals(want),
            reason: 'seed $seed p $p signer set mismatch: got=$got want=$want');
      }
    }

    // Verify the generator actually emits revoke-signer ops (the critical fold path).
    expect(totalRevokes, greaterThan(0),
        reason: 'generator must emit at least one revoke-signer op across 40 seeds');
  });
}
