import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/e2e/trustlog/codec.dart' show hashEntry, unmarshalEntry;
import 'package:argus/transport/connection.dart' show RpcLink;
import 'package:argus/transport/jsonrpc.dart';

import 'loopback.dart';

Map<String, dynamic> _tl() =>
    (jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>;

Uint8List _b(Map<String, dynamic> v, String k) =>
    Uint8List.fromList(base64.decode(v[k] as String));

// Minimal RpcLink whose gateway responses are driven by [onSend].
class _CallbackLink implements RpcLink {
  _CallbackLink(this._onSend) : _ctrl = StreamController<RpcMessage>();

  final void Function(Map<String, dynamic> j, _CallbackLink self) _onSend;
  final StreamController<RpcMessage> _ctrl;

  void push(Object response) =>
      _ctrl.add(RpcMessage.fromJson(jsonDecode(jsonEncode(response)) as Map<String, dynamic>));

  @override
  Stream<RpcMessage> get incoming => _ctrl.stream;

  @override
  void send(String frame) {
    for (final part in frame.split('\n')) {
      if (part.trim().isEmpty) continue;
      _onSend(jsonDecode(part) as Map<String, dynamic>, this);
    }
  }

  @override
  Future<void> close() async {
    if (!_ctrl.isClosed) await _ctrl.close();
  }
}

void main() {
  group('trustlog.sync invariant', () {
    test('loss scenario: retained entries enable assembly of a partial delta', () async {
      // Client starts with fork_chain=[genesis,authB] as its trust anchor.
      // Phase 1 (connect): gateway sends chain=[genesis,authA]; client assembles
      //   both chain and fork_chain (fork_chain wins, trust.chainBytes=fork_chain),
      //   retains authA in _seenBranches.
      // Phase 2 (resyncNow): gateway sends ONLY the disable entry (prev=authA).
      //   Client must reconstruct disabled_chain=[genesis,authA,disable] from the
      //   retained authA — which is absent from trust.chainBytes (=fork_chain).
      //
      // Without retention authA is missing from the merge; disable has no reachable
      // ancestor and disabled_chain cannot be assembled.
      final v = _tl();
      final chainXRaw = _b(v, 'chain');            // [genesis, authA]
      final chainYRaw = _b(v, 'fork_chain');        // [genesis, authB]
      final disabledRaw = _b(v, 'disabled_chain'); // [genesis, authA, disable]
      final genesisHash = _b(v, 'genesis_head');

      final chainXEntries = chainEntries(chainXRaw);
      final disableEntryB64 = base64.encode(chainEntries(disabledRaw)[2]); // just disable

      int syncPhase = 0;
      final link = _CallbackLink((j, self) {
        final id = j['id'];
        switch (j['method'] as String?) {
          case 'nodes.list':
            self.push({'jsonrpc': '2.0', 'id': id, 'result': {'nodes': []}});
          case 'trustlog.sync':
            // Phase 0 (connect's sync): send chain=[genesis,authA] so client
            // retains authA. Phase 1+ (resyncNow): send ONLY the disable entry.
            final entries = syncPhase == 0
                ? [for (final e in chainXEntries) base64.encode(e)]
                : [disableEntryB64];
            syncPhase++;
            self.push({
              'jsonrpc': '2.0',
              'id': id,
              'result': {'entries': entries, 'want': []},
            });
        }
      });

      // Seed with fork_chain so trust.chainBytes=fork_chain after ingest.
      // authA is therefore NOT in trust.chainBytes — only in the entry store.
      final client = E2EClient(
        link.incoming,
        link.send,
        await generateKeyPair(),
        genesisHash: genesisHash,
        initialTrustChain: chainYRaw,
      );

      // Phase 1: connect seeds fork_chain, then syncs and gets chain=[genesis,authA].
      await client.connect();
      // Phase 2: resyncNow sends only the disable entry.
      // The client rebuilds disabled_chain using retained authA.
      await client.resyncNow();

      expect(client.isDisabled, isTrue,
          reason: 'disabled_chain must be assembled and ingested '
              'using authA retained from phase 1; '
              'without retention authA is absent and assembly fails');

      await client.close();
    });

    test('rejected same-genesis branch is not re-downloaded on the next sync', () async {
      // Client starts seeded with chain=[genesis,authA] as its trust anchor.
      // First sync: gateway returns fork_chain=[genesis,authB] (same genesis, loses fork-choice).
      // Second sync: the offer's known must include authB's hash so the gateway
      // does not re-send it, proving retention works.
      final v = _tl();
      final chainRaw = _b(v, 'chain');           // [genesis, authA] — winner
      final forkChainRaw = _b(v, 'fork_chain'); // [genesis, authB] — same genesis
      final genesisHash = _b(v, 'genesis_head');

      final forkEntries = chainEntries(forkChainRaw);

      int syncPhase = 0;
      Map<String, dynamic>? capturedSecondOffer;

      final link = _CallbackLink((j, self) {
        final id = j['id'];
        switch (j['method'] as String?) {
          case 'nodes.list':
            self.push({'jsonrpc': '2.0', 'id': id, 'result': {'nodes': []}});
          case 'trustlog.sync':
            final params = j['params'];
            if (syncPhase == 0) {
              // First sync: return fork_chain entries so client retains authB.
              self.push({
                'jsonrpc': '2.0',
                'id': id,
                'result': {'entries': [for (final e in forkEntries) base64.encode(e)], 'want': []},
              });
            } else {
              capturedSecondOffer = params is Map<String, dynamic> ? params : null;
              self.push({'jsonrpc': '2.0', 'id': id, 'result': {'entries': [], 'want': []}});
            }
            syncPhase++;
        }
      });

      // Seed with chain so trust.chainBytes=chain after ingest. authB is therefore
      // NOT in trust.chainBytes — only in the entry store after first sync.
      final client = E2EClient(
        link.incoming,
        link.send,
        await generateKeyPair(),
        genesisHash: genesisHash,
        initialTrustChain: chainRaw,
      );

      await client.connect();   // first sync: gets fork_chain, retains authB
      await client.resyncNow(); // second sync: offer must include authB's hash

      expect(capturedSecondOffer, isNotNull);
      final knownB64 = (capturedSecondOffer!['known'] as List?)?.cast<String>() ?? <String>[];
      final knownSet = <String>{...knownB64};

      for (final entry in forkEntries) {
        final e = unmarshalEntry(entry);
        final hashB64 = base64.encode(hashEntry(e));
        expect(knownSet, contains(hashB64),
            reason: 'second offer must include rejected-fork entry hash to avoid re-download');
      }

      await client.close();
    });

    test('disjoint response does not crash on repeat — latch holds', () async {
      // Verifies the structural invariant of lastDisjointLogged: a gateway that
      // returns disjoint=true on every sync must not cause an error on the
      // second call (the latch prevents double-logging the warning).
      final v = _tl();
      final genesisHash = _b(v, 'genesis_head');

      final link = _CallbackLink((j, self) {
        final id = j['id'];
        switch (j['method'] as String?) {
          case 'nodes.list':
            self.push({'jsonrpc': '2.0', 'id': id, 'result': {'nodes': []}});
          case 'trustlog.sync':
            self.push({
              'jsonrpc': '2.0',
              'id': id,
              'result': {'entries': [], 'want': [], 'disjoint': true},
            });
        }
      });

      final client = E2EClient(
        link.incoming,
        link.send,
        await generateKeyPair(),
        genesisHash: genesisHash,
      );

      await client.connect();   // first disjoint sync — latch set
      await client.resyncNow(); // second disjoint sync — latch suppresses re-log

      await client.close();
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
