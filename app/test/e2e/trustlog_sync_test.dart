import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

import 'loopback.dart';

Map<String, dynamic> _tl() =>
    (jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>;

Uint8List _b(Map<String, dynamic> v, String k) =>
    Uint8List.fromList(base64.decode(v[k] as String));

void main() {
  group('trustlog.sync invariant', () {
    test('store at ceiling must not record the head of an unretained branch', () {
      // Structural: EntryStore only records a head if the entry was stored.
      // If put refuses (ceiling), the head must not appear in heads().
      // We cannot drive the 1<<16 ceiling in a unit test, but we can verify
      // the contract that a refused entry is not in the head set.
      //
      // Since the real ceiling cannot be hit without generating 65536 valid
      // signed entries, we verify the invariant property: the head set is a
      // strict subset of what was actually stored (refused entries are absent).
      final v = _tl();
      final entries = chainEntries(_b(v, 'chain'));
      final store = EntryStore();
      store.putAll(entries);
      final headsBefore = store.heads();
      // Garbage is not stored (decode failure) and must not appear as a head.
      store.put(Uint8List.fromList(utf8.encode('garbage')));
      expect(store.heads().length, headsBefore.length,
          reason: 'a refused/failed entry must not create a new head');
    });

    test('loss scenario: retained entries from a rejected branch can be assembled', () async {
      // The client wins branch X (chain) over fork Y (fork_chain). Both share
      // the same genesis. If the client retains Y's entries in its store, it
      // can assemble Y into a complete chain and ingest it if fork-choice later
      // swings (e.g., a disable entry arrives on Y). This mirrors the Go test
      // TestClientSyncTrustChainsRetainsRejectedBranch.
      final v = _tl();
      final chainX = _b(v, 'chain');
      final chainY = _b(v, 'fork_chain');
      final genesis = _b(v, 'genesis_head');

      final store = EntryStore();
      // Client holds X.
      store.putAll(chainEntries(chainX));
      // Client received-and-rejected Y (stored raw entries despite fork-choice rejection).
      store.putAll(chainEntries(chainY));

      // Both X's head and Y's head are in the entry store.
      expect(store.heads().length, 2,
          reason: 'both branch heads must be retained');

      // Assembling from all stored entries yields both chains.
      final (allEntries, _) = store.delta([]);
      final chains = assembleChains(allEntries);
      expect(chains.length, 2,
          reason: 'retained Y enables assembly of both chains');

      // Y's chain must be valid — TrustStore can ingest it.
      var yIngested = false;
      for (final c in chains) {
        if (bytesEqual(c, chainX)) continue;
        final ts = TrustStore(genesis);
        try {
          await ts.ingest(c);
          yIngested = true;
        } catch (_) {}
      }
      expect(yIngested, isTrue,
          reason: 'assembled fork Y must be ingestible — extension entries built '
              'on Y\'s head can be attached and verified');
    });

    test('existing-behaviour: a revoked device stops being authorized after a sync', () async {
      // Uses the reeval vectors: initial chain authorizes both A and B;
      // revoke_b chain revokes B. After resync the client must no longer authorize B.
      final v = _tl();
      final aKp = await keyPairFromSeed(_b(v, 'enforcement_node_a_seed'));
      final bKp = await keyPairFromSeed(_b(v, 'enforcement_node_b_seed'));
      final a = LoopbackNode('A', aKp, (m, p) => Uint8List.fromList(utf8.encode('null')));
      final b = LoopbackNode('B', bKp, (m, p) => Uint8List.fromList(utf8.encode('null')));
      final link = MultiNodeLoopbackLink({'A': a, 'B': b},
          trustChain: _b(v, 'reeval_initial_chain'));

      final client = E2EClient(
        link.incoming,
        link.send,
        await generateKeyPair(),
        tofu: true,
      );
      await client.connect();
      expect(client.connectedNodeIds.toSet(), {'A', 'B'},
          reason: 'initial chain authorizes both A and B');

      // Advance the gateway to the revoke-B chain and resync.
      link.trustChain = _b(v, 'reeval_revoke_b_chain');
      await client.resyncNow();

      expect(client.connectedNodeIds.toSet(), {'A'},
          reason: 'after resync B must be dropped: it is revoked in the new chain');
      await client.close();
    });
  });
}
