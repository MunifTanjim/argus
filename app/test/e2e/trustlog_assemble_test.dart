import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

Map<String, dynamic> _tl() =>
    (jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>;

Uint8List _b(Map<String, dynamic> v, String k) =>
    Uint8List.fromList(base64.decode(v[k] as String));

void main() {
  group('chainEntries + assembleChains', () {
    test('complete chain round-trips to byte-identical output', () async {
      final v = _tl();
      final chain = _b(v, 'chain');

      final raw = chainEntries(chain);
      expect(raw.length, greaterThanOrEqualTo(2));

      final chains = assembleChains(raw);
      expect(chains.length, 1, reason: 'one complete chain');
      expect(chains[0], equals(chain),
          reason: 'assembled bytes must be byte-identical to original');

      // The assembled bytes must also be accepted by TrustStore.ingest.
      final genesis = _b(v, 'genesis_head');
      final store = TrustStore(genesis);
      final advanced = await store.ingest(chains[0]);
      expect(advanced, isTrue);
    });

    test('branch missing its genesis is skipped', () {
      final v = _tl();
      final raw = chainEntries(_b(v, 'chain'));
      expect(raw.length, greaterThanOrEqualTo(2));
      // Drop the genesis: the remaining entry cannot walk back to a nil-Prev root.
      final chains = assembleChains(raw.sublist(1));
      expect(chains, isEmpty,
          reason: 'chain with a hole must be skipped');
    });

    test('duplicates collapse to one chain', () async {
      final v = _tl();
      final chain = _b(v, 'chain');
      final raw = chainEntries(chain);
      // Supply the entries twice.
      final chains = assembleChains([...raw, ...raw]);
      expect(chains.length, 1, reason: 'duplicate entries must collapse');
      expect(chains[0], equals(chain));
    });

    test('undecodable entries are ignored and the valid chain still assembles', () async {
      final v = _tl();
      final chain = _b(v, 'chain');
      final raw = chainEntries(chain);
      final garbage = Uint8List.fromList(utf8.encode('not an entry'));
      final chains = assembleChains([...raw, garbage]);
      expect(chains.length, 1);
      expect(chains[0], equals(chain));
    });

    test('two competing forks assemble as two separate chains', () async {
      final v = _tl();
      final chainX = _b(v, 'chain');
      final chainY = _b(v, 'fork_chain');
      final rawX = chainEntries(chainX);
      final rawY = chainEntries(chainY);
      final chains = assembleChains([...rawX, ...rawY]);
      // Genesis is shared; each fork yields one chain.
      expect(chains.length, 2,
          reason: 'two competing forks assemble as two separate chains');
      // Both must be ingested by the TOFU store (first adopt pins genesis).
      final store = TrustStore.tofu();
      for (final c in chains) {
        try {
          await store.ingest(c);
        } catch (_) {}
      }
      expect(store.tip, isNotNull,
          reason: 'at least one chain must be adopted');
    });

    test('chainEntries rejects garbage input', () {
      expect(
          () => chainEntries(Uint8List.fromList(utf8.encode('not a chain'))),
          throwsFormatException);
    });

    test('disabled chain round-trips', () async {
      final v = _tl();
      final chain = _b(v, 'disabled_chain');
      final genesis = _b(v, 'genesis_head');
      final raw = chainEntries(chain);
      final chains = assembleChains(raw);
      expect(chains.length, 1);
      expect(chains[0], equals(chain));
      final store = TrustStore(genesis);
      await store.ingest(chains[0]);
      expect(store.disabled, isTrue);
    });
  });
}
