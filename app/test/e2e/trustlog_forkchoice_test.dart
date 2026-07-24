import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

/// Loads the fork_choice section from the shared Go↔Dart golden vectors file.
Map<String, dynamic> _fc() =>
    (jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>)['fork_choice'] as Map<String, dynamic>;

Uint8List _b(Map<String, dynamic> v, String k) =>
    Uint8List.fromList(base64.decode(v[k] as String));

void main() {
  // --- (a) co-signed shorter branch beats longer plain branch ---
  group('cosigned_shorter_beats_longer', () {
    late Map<String, dynamic> v;
    setUp(() => v = _fc()['cosigned_shorter_beats_longer'] as Map<String, dynamic>);

    test('longer plain branch ingested first — shorter co-signed branch must win', () async {
      final store = TrustStore(_b(v, 'genesis_hash'));
      expect(await store.ingest(_b(v, 'cur')), isTrue);    // adopt longer plain branch
      await store.ingest(_b(v, 'cand'));                    // co-signed shorter must be adopted
      expect(store.tip, equals(_b(v, 'winner_tip')));
      expect(store.signerTrusted(_b(v, 'loser_signer_not_trusted')), isFalse,
          reason: 'revoked signer must not be trusted');
      expect(store.deviceAuthorized(_b(v, 'loser_dev_a')), isFalse,
          reason: 'device authorized by revoked signer must be invalidated');
      expect(store.deviceAuthorized(_b(v, 'loser_dev_b')), isFalse,
          reason: 'device authorized by revoked signer must be invalidated');
    });

    test('co-signed branch ingested first — plain branch loses to it', () async {
      final store = TrustStore(_b(v, 'genesis_hash'));
      expect(await store.ingest(_b(v, 'cand')), isTrue);   // adopt shorter co-signed branch
      await store.ingest(_b(v, 'cur'));                     // longer plain must lose (no-op)
      expect(store.tip, equals(_b(v, 'winner_tip')));
      expect(store.signerTrusted(_b(v, 'loser_signer_not_trusted')), isFalse);
    });
  });

  // --- (b) puppet-attack: higher co-sign count post-fork still loses ---
  group('puppet_attack', () {
    late Map<String, dynamic> v;
    setUp(() => v = _fc()['puppet_attack'] as Map<String, dynamic>);

    void assertHonestWins(TrustStore store) {
      expect(store.tip, equals(_b(v, 'winner_tip')),
          reason: 'honest branch must be the winner');
      expect(store.signerTrusted(_b(v, 'winner_signer_a')), isTrue,
          reason: 'honest signer A must stay trusted');
      expect(store.signerTrusted(_b(v, 'winner_signer_b')), isTrue,
          reason: 'honest signer B must stay trusted');
      expect(store.signerTrusted(_b(v, 'loser_signer_c')), isFalse,
          reason: 'compromised signer C must be revoked');
      expect(store.signerTrusted(_b(v, 'puppet_p1')), isFalse,
          reason: 'puppet P1 must never be trusted');
      expect(store.signerTrusted(_b(v, 'puppet_p2')), isFalse,
          reason: 'puppet P2 must never be trusted');
      expect(store.signerTrusted(_b(v, 'puppet_p3')), isFalse,
          reason: 'puppet P3 must never be trusted');
    }

    test('honest first, attacker ingested over it', () async {
      final store = TrustStore(_b(v, 'genesis_hash'));
      await store.ingest(_b(v, 'honest_chain'));
      await store.ingest(_b(v, 'attacker_chain'));
      assertHonestWins(store);
    });

    test('attacker first, honest ingested over it (puppet attack must fail)', () async {
      final store = TrustStore(_b(v, 'genesis_hash'));
      await store.ingest(_b(v, 'attacker_chain'));
      await store.ingest(_b(v, 'honest_chain'));
      assertHonestWins(store);
    });
  });

  // --- (c) tie-break: winner = branch with lower first-diverging-entry hash ---
  group('tiebreak', () {
    late Map<String, dynamic> v;
    setUp(() => v = _fc()['tiebreak'] as Map<String, dynamic>);

    test('x then y — winner is the same', () async {
      final store = TrustStore(_b(v, 'genesis_hash'));
      await store.ingest(_b(v, 'chain_x'));
      await store.ingest(_b(v, 'chain_y'));
      expect(store.tip, equals(_b(v, 'winner_tip')));
    });

    test('y then x — same winner (order-independent)', () async {
      final store = TrustStore(_b(v, 'genesis_hash'));
      await store.ingest(_b(v, 'chain_y'));
      await store.ingest(_b(v, 'chain_x'));
      expect(store.tip, equals(_b(v, 'winner_tip')));
    });
  });

  // --- (d) revoke-signer with replacement: co-signed+replacement beats plain longer branch ---
  group('revoke_with_replacement', () {
    late Map<String, dynamic> v;
    setUp(() => v = _fc()['revoke_with_replacement'] as Map<String, dynamic>);

    void assertHonestWins(TrustStore store) {
      expect(store.tip, equals(_b(v, 'winner_tip')),
          reason: 'honest branch (revoke+replacement) must win');
      expect(store.signerTrusted(_b(v, 'winner_signer_a')), isTrue,
          reason: 'signer A must stay trusted');
      expect(store.signerTrusted(_b(v, 'winner_signer_b')), isTrue,
          reason: 'signer B must stay trusted');
      expect(store.signerTrusted(_b(v, 'winner_signer_d')), isTrue,
          reason: 'replacement signer D must be trusted');
      expect(store.signerTrusted(_b(v, 'loser_signer_c')), isFalse,
          reason: 'revoked signer C must not be trusted');
      expect(store.deviceAuthorized(_b(v, 'loser_dev_c')), isFalse,
          reason: 'devC authorized by revoked C must be invalidated');
    }

    test('c-branch ingested first — honest revoke-with-replacement must win', () async {
      final store = TrustStore(_b(v, 'genesis_hash'));
      await store.ingest(_b(v, 'c_branch'));
      await store.ingest(_b(v, 'honest_chain'));
      assertHonestWins(store);
    });

    test('honest ingested first — c-branch must lose (no-op)', () async {
      final store = TrustStore(_b(v, 'genesis_hash'));
      await store.ingest(_b(v, 'honest_chain'));
      await store.ingest(_b(v, 'c_branch'));
      assertHonestWins(store);
    });
  });

  // --- (e) disablement dominance: a break-glass disabled chain beats a
  // non-disabled competitor regardless of fork weight and ingest order ---
  group('disablement_dominance', () {
    late Map<String, dynamic> v;
    setUp(() => v = _fc()['disablement_dominance'] as Map<String, dynamic>);

    test('attacker adopted first — disabled chain must dominate and be adopted', () async {
      final store = TrustStore(_b(v, 'genesis_hash'));
      await store.ingest(_b(v, 'attacker_chain'));
      await store.ingest(_b(v, 'disabled_chain'));
      expect(store.disabled, isTrue, reason: 'disabled chain must win fork-choice');
      expect(store.tip, equals(_b(v, 'winner_tip')));
      expect(store.signerTrusted(_b(v, 'added_signer_b')), isFalse,
          reason: 'attacker-added signer must not be trusted after the disable wins');
    });

    test('disabled adopted first — a non-disabled branch must not roll it back', () async {
      final store = TrustStore(_b(v, 'genesis_hash'));
      await store.ingest(_b(v, 'disabled_chain'));
      await store.ingest(_b(v, 'attacker_chain'));
      expect(store.disabled, isTrue, reason: 'a disablement must not be rolled back');
      expect(store.tip, equals(_b(v, 'winner_tip')));
      expect(store.signerTrusted(_b(v, 'added_signer_b')), isFalse);
    });
  });
}
