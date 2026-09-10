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

Uint8List _b(Map<String, dynamic> v, String k) =>
    Uint8List.fromList(base64.decode(v[k] as String));

Uint8List _genesisOf(Uint8List chain) =>
    hashEntry(unmarshalEntry(chainEntries(chain).first));

class _CallbackLink implements RpcLink {
  _CallbackLink(this._onSend) : _ctrl = StreamController<RpcMessage>();
  final void Function(Map<String, dynamic> j, _CallbackLink self) _onSend;
  final StreamController<RpcMessage> _ctrl;
  void push(Object response) => _ctrl.add(
      RpcMessage.fromJson(jsonDecode(jsonEncode(response)) as Map<String, dynamic>));
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
  test('surfaces a superseding root when the pinned root is disabled, and adopts it',
      () async {
    // The app is pinned to genesis G (`chain`). The gateway serves the disable
    // entry for G (killing the app's own root) plus a live successor under a
    // different genesis (`wrong_genesis_chain`).
    final v = _tl();
    final ownChain = _b(v, 'chain'); // [genesis G, authA]
    final disabledChain = _b(v, 'disabled_chain'); // [G, authA, disable]
    final liveChain = _b(v, 'wrong_genesis_chain'); // different genesis, live
    final genesisG = _b(v, 'genesis_head');

    final disableEntry = base64.encode(chainEntries(disabledChain)[2]);
    final liveEntries = [for (final e in chainEntries(liveChain)) base64.encode(e)];

    final link = _CallbackLink((j, self) {
      final id = j['id'];
      switch (j['method'] as String?) {
        case 'nodes.list':
          self.push({'jsonrpc': '2.0', 'id': id, 'result': {'nodes': []}});
        case 'trustlog.sync':
          self.push({
            'jsonrpc': '2.0',
            'id': id,
            'result': {'entries': [disableEntry, ...liveEntries], 'want': []},
          });
      }
    });

    Uint8List? persisted;
    final client = E2EClient(
      link.incoming,
      link.send,
      await generateKeyPair(),
      genesisHash: genesisG,
      initialTrustChain: ownChain,
      onTrustChainAdvance: (chain) async => persisted = chain,
    );

    await client.connect();
    await client.resyncNow();

    expect(client.isDisabled, isTrue, reason: 'own root G was disabled');
    expect(client.supersededByGenesis, equals(_genesisOf(liveChain)),
        reason: 'names the live successor root');
    expect(client.canAdoptSupersedingRoot, isTrue);

    final adopted = await client.adoptSupersedingRoot();

    expect(adopted, isTrue);
    expect(client.isDisabled, isFalse, reason: 're-pinned to the live root');
    expect(client.supersededByGenesis, isNull, reason: 'supersession cleared after adopt');
    expect(client.canAdoptSupersedingRoot, isFalse);
    expect(persisted, equals(liveChain), reason: 'new anchor persisted');

    await client.close();
  });

  test('does not flag supersession while the pinned root is healthy', () async {
    final v = _tl();
    final ownChain = _b(v, 'chain');
    final genesisG = _b(v, 'genesis_head');
    final ownEntries = [for (final e in chainEntries(ownChain)) base64.encode(e)];

    final link = _CallbackLink((j, self) {
      final id = j['id'];
      switch (j['method'] as String?) {
        case 'nodes.list':
          self.push({'jsonrpc': '2.0', 'id': id, 'result': {'nodes': []}});
        case 'trustlog.sync':
          self.push({
            'jsonrpc': '2.0',
            'id': id,
            'result': {'entries': ownEntries, 'want': []},
          });
      }
    });

    final client = E2EClient(
      link.incoming,
      link.send,
      await generateKeyPair(),
      genesisHash: genesisG,
      initialTrustChain: ownChain,
    );

    await client.connect();
    await client.resyncNow();

    expect(client.isDisabled, isFalse);
    expect(client.supersededByGenesis, isNull);
    expect(client.canAdoptSupersedingRoot, isFalse);

    await client.close();
  });
}
