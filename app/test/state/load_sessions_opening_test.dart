import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/sessions.dart';

import '../e2e/loopback.dart';

Map<String, dynamic> _session(String node) => {
  'id': '$node:s',
  'node_id': node,
  'name': 'old',
  'tmux': _tmux,
};

const _tmux = {
  'server': 'argus',
  'pane_id': '%1',
  'session_name': 's',
  'window_index': 0,
  'current_path': '/p',
};

void main() {
  test(
    'loadSessions keeps an opening node\'s sessions and drops a failed one',
    () async {
      final gate = Completer<void>();
      List<int> handler(String m, Uint8List p) => Uint8List.fromList(
        utf8.encode(
          jsonEncode([
            {'id': 's', 'name': 'new', 'tmux': _tmux},
          ]),
        ),
      );
      final a = LoopbackNode('A', await generateKeyPair(), handler);
      final b = LoopbackNode('B', await generateKeyPair(), handler)
        ..handshakeGate = gate;
      final c = LoopbackNode('C', await generateKeyPair(), handler);
      final lnk = MultiNodeLoopbackLink(
        {'A': a, 'B': b, 'C': c},
        failRelayOpen: {'C'},
      );
      final client = E2EClient(lnk.incoming, lnk.send, await generateKeyPair());
      addTearDown(client.close);

      final container = ProviderContainer();
      addTearDown(container.dispose);
      final store = container.read(sessionsProvider.notifier);
      store.mergeNode('B', parseSessions([_session('B')]));
      store.mergeNode('C', parseSessions([_session('C')]));

      await client.connect();
      final load = loadSessions(client, store);
      for (var i = 0; i < 20; i++) {
        await Future(() {});
      }
      expect(container.read(sessionsProvider).keys, contains('B:s'));
      expect(container.read(sessionsProvider)['B:s']!.name, 'old');

      gate.complete();
      await load;
      final sessions = container.read(sessionsProvider);
      expect(sessions.keys, containsAll(['A:s', 'B:s']));
      expect(sessions['B:s']!.name, 'new');
      expect(sessions.keys, isNot(contains('C:s')));
    },
  );
}
