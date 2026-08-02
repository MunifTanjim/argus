import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/transport/connection.dart' show RpcLink;
import 'package:argus/transport/jsonrpc.dart';

Map<String, dynamic> _tl() =>
    (jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>;

// Minimal gateway link whose trustlog.sync response is controlled by the test.
class _OrphanGateway implements RpcLink {
  _OrphanGateway() : _ctrl = StreamController<RpcMessage>();

  final StreamController<RpcMessage> _ctrl;

  // Entries returned on the next trustlog.sync call.
  List<String> nextEntries = [];

  void _respond(Object r) =>
      _ctrl.add(RpcMessage.fromJson(jsonDecode(jsonEncode(r)) as Map<String, dynamic>));

  @override
  Stream<RpcMessage> get incoming => _ctrl.stream;

  @override
  void send(String frame) {
    for (final part in frame.split('\n')) {
      if (part.trim().isEmpty) continue;
      final j = jsonDecode(part) as Map<String, dynamic>;
      final id = j['id'];
      switch (j['method'] as String?) {
        case 'nodes.list':
          _respond({'jsonrpc': '2.0', 'id': id, 'result': {'nodes': []}});
        case 'trustlog.sync':
          _respond({
            'jsonrpc': '2.0',
            'id': id,
            'result': {'entries': nextEntries, 'want': <String>[]},
          });
        case 'ping':
          _respond({'jsonrpc': '2.0', 'id': id, 'result': null});
      }
    }
  }

  @override
  Future<void> close() async {
    if (!_ctrl.isClosed) await _ctrl.close();
  }
}

void main() {
  // Verify that the unplaced-warning state variable (_lastUnplacedLogged) is
  // updated on every sync and that the change-detection logic works correctly:
  // the warning fires when the count changes, not on every occurrence.
  // Asserting on debugLastUnplacedLogged rather than developer.log output because
  // Dart's developer.log goes to the timeline/inspector, not a capturable stream.
  test('unplaced warning fires on change, not on every occurrence', () async {
    final v = _tl();
    final genesisHash = Uint8List.fromList(base64.decode(v['genesis_head'] as String));

    // Orphan entry: take the second entry from chain (prev = genesis, but genesis
    // is absent from the served entries → unplaceable).
    final chainRaw = Uint8List.fromList(base64.decode(v['chain'] as String));
    final allEntries = chainEntries(chainRaw); // [genesis, authorize]
    final orphan1 = base64.encode(allEntries[1]); // just the authorize entry

    // A second orphan from disabled_chain (the disable entry, prev = authA).
    final disabledRaw = Uint8List.fromList(base64.decode(v['disabled_chain'] as String));
    final disabledEntries = chainEntries(disabledRaw); // [genesis, authA, disable]
    final orphan2 = base64.encode(disabledEntries[2]); // the disable entry

    final gw = _OrphanGateway();
    final client = E2EClient(gw.incoming, gw.send, await generateKeyPair(),
        genesisHash: genesisHash);

    // connect() calls _syncTrustLog with empty entries → unplaced stays 0.
    await client.connect();
    expect(client.debugLastUnplacedLogged, 0,
        reason: 'no orphans yet: lastUnplacedLogged must be 0');

    // First resync: gateway serves 1 orphan → unplaced changes 0→1, warning fires.
    gw.nextEntries = [orphan1];
    await client.resyncNow();
    expect(client.debugLastUnplacedLogged, 1,
        reason: 'first orphan: lastUnplacedLogged must advance to 1');

    // Second resync: same orphan (already retained, deduped) → unplaced still 1,
    // warning suppressed (count unchanged).
    gw.nextEntries = [orphan1];
    await client.resyncNow();
    expect(client.debugLastUnplacedLogged, 1,
        reason: 'identical count: lastUnplacedLogged stays 1 (no repeated warning)');

    // Third resync: a second orphan entry added → unplaced changes 1→2, warning fires.
    gw.nextEntries = [orphan2];
    await client.resyncNow();
    expect(client.debugLastUnplacedLogged, 2,
        reason: 'count changed 1→2: lastUnplacedLogged must advance to 2');

    await client.close();
  });
}
