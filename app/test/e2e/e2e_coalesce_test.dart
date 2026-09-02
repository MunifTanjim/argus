import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

import 'loopback.dart';

void main() {
  test('a single trust-changed nudge runs exactly one resync', () async {
    final a = LoopbackNode(
      'A',
      await generateKeyPair(),
      (m, p) => utf8.encode('null'),
    );
    final link = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(
      link.incoming,
      link.send,
      await generateKeyPair(),
      tofu: true,
    );
    await client.connect();
    final base = link.trustSyncCount;

    link.pushNotification('node.event', {'type': 'trust-changed'});

    final deadline = DateTime.now().add(const Duration(seconds: 3));
    while (DateTime.now().isBefore(deadline)) {
      await Future<void>.delayed(const Duration(milliseconds: 20));
      if (link.trustSyncCount > base) break;
    }

    expect(
      link.trustSyncCount - base,
      equals(1),
      reason: 'one nudge must trigger exactly one resync',
    );
    await client.close();
  });

  test('a burst of trust-changed nudges coalesces to at most two resyncs', () async {
    final a = LoopbackNode(
      'A',
      await generateKeyPair(),
      (m, p) => utf8.encode('null'),
    );
    final link = MultiNodeLoopbackLink({'A': a});
    final client = E2EClient(
      link.incoming,
      link.send,
      await generateKeyPair(),
      tofu: true,
    );
    await client.connect();
    final base = link.trustSyncCount;

    // Fire 5 nudges synchronously before the event loop can process any of them.
    // The first _kickResync sets _resyncInFlight; the rest set _resyncPending only.
    for (var i = 0; i < 5; i++) {
      link.pushNotification('node.event', {'type': 'trust-changed'});
    }

    // Poll until the count stabilizes, then verify the cap.
    final deadline = DateTime.now().add(const Duration(seconds: 5));
    var stable = 0;
    var last = link.trustSyncCount;
    while (DateTime.now().isBefore(deadline)) {
      await Future<void>.delayed(const Duration(milliseconds: 50));
      final cur = link.trustSyncCount;
      if (cur == last) {
        stable++;
        if (stable >= 4) break; // four consecutive stable reads → settled
      } else {
        stable = 0;
        last = cur;
      }
    }

    expect(
      link.trustSyncCount - base,
      lessThanOrEqualTo(2),
      reason:
          'a burst of trust-changed nudges must collapse to at most 2 resyncs '
          '(one in-flight + one queued follow-up)',
    );
    expect(
      link.trustSyncCount - base,
      greaterThanOrEqualTo(1),
      reason: 'at least one resync must run',
    );
    await client.close();
  });
}
