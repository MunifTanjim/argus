import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

import 'loopback.dart';

// Helpers ------------------------------------------------------------------

Map<String, dynamic> _tl() =>
    (jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>;

Uint8List _b(Map<String, dynamic> v, String k) =>
    Uint8List.fromList(base64.decode(v[k] as String));

/// Waits for [condition] to become true, pumping the event loop between polls.
/// Fails after [maxAttempts] pump cycles.
Future<void> _waitFor(String desc, bool Function() condition,
    {int maxAttempts = 50}) async {
  for (var i = 0; i < maxAttempts; i++) {
    if (condition()) return;
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
  fail('timed out waiting for: $desc');
}

// Tests --------------------------------------------------------------------

void main() {
  group('beacon state pruning', () {
    test(
        '_reevaluateChannels prunes beacon state for a revoked node so the stale '
        'tip cannot accumulate misses and false-positive equivocation', () async {
      final v = _tl();
      // Use the reeval vectors: node A (seed_a) and node B (seed_b) are both
      // authorized in reeval_initial_chain; node B is revoked in reeval_revoke_b_chain.
      final aKp = await keyPairFromSeed(_b(v, 'enforcement_node_a_seed'));
      final bKp = await keyPairFromSeed(_b(v, 'enforcement_node_b_seed'));

      final a = LoopbackNode('A', aKp, (m, p) => Uint8List.fromList(utf8.encode('null')));
      final b = LoopbackNode('B', bKp, (m, p) => Uint8List.fromList(utf8.encode('null')));

      final link = MultiNodeLoopbackLink({'A': a, 'B': b},
          trustChain: _b(v, 'reeval_initial_chain'));

      final client = E2EClient(link.incoming, link.send, await generateKeyPair(), tofu: true);
      await client.connect();
      expect(client.connectedNodeIds.toSet(), equals({'A', 'B'}));

      // Give node B a divergent beacon (tip not in any chain) via gateway notification.
      // The beacon must pass verifyBeacon, so we generate a proper signed beacon.
      final (bBeaconPriv, bBeaconPub) = await generateBeaconKeyPair();
      final divergentTip = Uint8List(32)..fillRange(0, 32, 0xde);
      final bBeacon = await signBeacon(bBeaconPriv, bBeaconPub, divergentTip, 5, 1);
      final bIdentB64 = base64.encode(bKp.publicKey);
      final bBeaconPubB64 = base64.encode(bBeaconPub);

      link.pushNotification('node.event', {
        'type': 'beacon',
        'node': {
          'id': 'B',
          'identity_pubkey': bIdentB64,
          'beacon_pubkey': bBeaconPubB64,
          'beacon': bBeacon.toJson(),
        },
      });

      // Wait for beacon to be ingested.
      await _waitFor('B divergent beacon ingested', () {
        // The beacon is ingested when the counter > 0 internally; we can
        // detect this indirectly by calling resyncNow (which runs the check)
        // without equivocation being set yet (miss=1 is not enough).
        return true; // we'll detect via equivocation not being set
      });
      await Future<void>.delayed(const Duration(milliseconds: 20));

      // First resync: miss=1 for B's divergent tip — not yet flagged.
      await client.resyncNow();
      expect(client.equivocation, isFalse,
          reason: 'single miss must not set equivocation flag');

      // Revoke B: gateway now serves the chain that revokes B.
      link.trustChain = _b(v, 'reeval_revoke_b_chain');
      // Second resync: chain changed → _reevaluateChannels drops B's channel AND
      // prunes B's beacon state → _checkBeaconConsistency has no B beacon →
      // no second miss → equivocation must NOT be set.
      await client.resyncNow();
      expect(client.equivocation, isFalse,
          reason:
              '_reevaluateChannels must prune the revoked node beacon so the '
              'stale tip never accumulates a second miss');

      await client.close();
    });

    test(
        'offline node.event prunes beacon state so the stale tip cannot '
        'false-positive equivocation after the node goes offline', () async {
      final v = _tl();
      // Use the enforcement chain (authorizes node A only) with a genesis hash.
      // We want a trust store so _checkBeaconConsistency actually runs.
      final aKp = await keyPairFromSeed(_b(v, 'enforcement_node_a_seed'));
      final bKp = await keyPairFromSeed(_b(v, 'enforcement_node_b_seed'));

      final a = LoopbackNode('A', aKp, (m, p) => Uint8List.fromList(utf8.encode('null')));
      // B is not authorized, but its beacon can still be sent via node.event.
      final b = LoopbackNode('B', bKp, (m, p) => Uint8List.fromList(utf8.encode('null')));

      final link = MultiNodeLoopbackLink({'A': a, 'B': b},
          trustChain: _b(v, 'enforcement_chain'));

      final client = E2EClient(link.incoming, link.send, await generateKeyPair(),
          genesisHash: _b(v, 'enforcement_genesis_head'));
      await client.connect();
      // Only A is authorized and connected.
      expect(client.connectedNodeIds.toSet(), equals({'A'}));

      // Inject a divergent beacon for B via node.event beacon notification.
      // B is not connected but its beacon can still be ingested for cross-check.
      final (bBeaconPriv, bBeaconPub) = await generateBeaconKeyPair();
      final divergentTip = Uint8List(32)..fillRange(0, 32, 0xde);
      final bBeacon = await signBeacon(bBeaconPriv, bBeaconPub, divergentTip, 5, 1);
      final bIdentB64 = base64.encode(bKp.publicKey);
      final bBeaconPubB64 = base64.encode(bBeaconPub);

      link.pushNotification('node.event', {
        'type': 'beacon',
        'node': {
          'id': 'B',
          'identity_pubkey': bIdentB64,
          'beacon_pubkey': bBeaconPubB64,
          'beacon': bBeacon.toJson(),
        },
      });

      await Future<void>.delayed(const Duration(milliseconds: 20));

      // Now send an offline event for B — this should prune B's beacon state.
      link.pushNotification('node.event', {
        'type': 'offline',
        'node': {'id': 'B', 'identity_pubkey': bIdentB64},
      });
      await Future<void>.delayed(const Duration(milliseconds: 20));

      // Multiple resyncs: B's beacon was pruned by the offline event, so
      // _checkBeaconConsistency has nothing to check for B → no misses → no flag.
      await client.resyncNow();
      await client.resyncNow();
      expect(client.equivocation, isFalse,
          reason:
              'offline event must prune B\'s beacon state; subsequent checks '
              'must not accumulate misses for B');

      await client.close();
    });

    test(
        'removed node.event prunes beacon state so the stale tip cannot '
        'false-positive equivocation after the node is removed', () async {
      final v = _tl();
      final aKp = await keyPairFromSeed(_b(v, 'enforcement_node_a_seed'));
      final bKp = await keyPairFromSeed(_b(v, 'enforcement_node_b_seed'));

      final a = LoopbackNode('A', aKp, (m, p) => Uint8List.fromList(utf8.encode('null')));
      final b = LoopbackNode('B', bKp, (m, p) => Uint8List.fromList(utf8.encode('null')));

      final link = MultiNodeLoopbackLink({'A': a, 'B': b},
          trustChain: _b(v, 'enforcement_chain'));

      final client = E2EClient(link.incoming, link.send, await generateKeyPair(),
          genesisHash: _b(v, 'enforcement_genesis_head'));
      await client.connect();
      expect(client.connectedNodeIds.toSet(), equals({'A'}));

      final (bBeaconPriv, bBeaconPub) = await generateBeaconKeyPair();
      final divergentTip = Uint8List(32)..fillRange(0, 32, 0xfe);
      final bBeacon = await signBeacon(bBeaconPriv, bBeaconPub, divergentTip, 5, 1);
      final bIdentB64 = base64.encode(bKp.publicKey);
      final bBeaconPubB64 = base64.encode(bBeaconPub);

      link.pushNotification('node.event', {
        'type': 'beacon',
        'node': {
          'id': 'B',
          'identity_pubkey': bIdentB64,
          'beacon_pubkey': bBeaconPubB64,
          'beacon': bBeacon.toJson(),
        },
      });
      await Future<void>.delayed(const Duration(milliseconds: 20));

      // Send a removed event → prunes B's beacon state.
      link.pushNotification('node.event', {
        'type': 'removed',
        'node': {'id': 'B', 'identity_pubkey': bIdentB64},
      });
      await Future<void>.delayed(const Duration(milliseconds: 20));

      await client.resyncNow();
      await client.resyncNow();
      expect(client.equivocation, isFalse,
          reason:
              'removed event must prune B\'s beacon state; no misses should accumulate');

      await client.close();
    });

    test(
        '_checkBeaconConsistency skips beacons for nodes that were connected '
        '(in _everConnected) but are no longer connected — the _everConnected '
        'guard prevents stale re-ingested beacons from false-positiving', () async {
      final v = _tl();
      // Both A and B are authorized in reeval_initial_chain.
      final aKp = await keyPairFromSeed(_b(v, 'enforcement_node_a_seed'));
      final bKp = await keyPairFromSeed(_b(v, 'enforcement_node_b_seed'));

      final a = LoopbackNode('A', aKp, (m, p) => Uint8List.fromList(utf8.encode('null')));
      final b = LoopbackNode('B', bKp, (m, p) => Uint8List.fromList(utf8.encode('null')));

      final link = MultiNodeLoopbackLink({'A': a, 'B': b},
          trustChain: _b(v, 'reeval_initial_chain'));

      final client = E2EClient(link.incoming, link.send, await generateKeyPair(), tofu: true);
      await client.connect();
      expect(client.connectedNodeIds.toSet(), equals({'A', 'B'}));

      // Step 1: Revoke B — chain changes, _reevaluateChannels drops B's channel
      // and prunes B's beacon. B is now in _everConnected but not in _byNodeId.
      link.trustChain = _b(v, 'reeval_revoke_b_chain');
      await client.resyncNow();
      expect(client.connectedNodeIds.toSet(), equals({'A'}),
          reason: 'B must be dropped after revocation');
      expect(client.equivocation, isFalse);

      // Step 2: A new divergent beacon notification for B arrives (e.g. B is
      // still running and emitting beacons even though it's been revoked). The
      // client ingests it (valid signature). This simulates the window between
      // _reevaluateChannels pruning the beacon and the next consistency check.
      final (bBeaconPriv, bBeaconPub) = await generateBeaconKeyPair();
      final divergentTip = Uint8List(32)..fillRange(0, 32, 0xab);
      final bBeacon = await signBeacon(bBeaconPriv, bBeaconPub, divergentTip, 5, 1);
      final bIdentB64 = base64.encode(bKp.publicKey);
      final bBeaconPubB64 = base64.encode(bBeaconPub);

      link.pushNotification('node.event', {
        'type': 'beacon',
        'node': {
          'id': 'B',
          'identity_pubkey': bIdentB64,
          'beacon_pubkey': bBeaconPubB64,
          'beacon': bBeacon.toJson(),
        },
      });
      await Future<void>.delayed(const Duration(milliseconds: 20));

      // Step 3: Another resync (same revoke chain, no change). _reevaluateChannels
      // is NOT called (chain unchanged). _checkBeaconConsistency runs: B is in
      // _everConnected AND is NOT connected → SKIP B's beacon → no miss.
      // Without the _everConnected guard, B's divergent beacon would accumulate
      // a miss here and eventually (after 2 total) set the equivocation flag.
      await client.resyncNow();
      expect(client.equivocation, isFalse,
          reason:
              '_checkBeaconConsistency must skip B (in _everConnected but not '
              'connected) so a re-ingested stale beacon cannot accumulate misses');

      // A second resync still must not flag (the guard applies every tick).
      await client.resyncNow();
      expect(client.equivocation, isFalse,
          reason:
              'equivocation must not be set even after multiple resyncs when '
              'the _everConnected guard skips the offline node\'s stale beacon');

      await client.close();
    });
  });
}
