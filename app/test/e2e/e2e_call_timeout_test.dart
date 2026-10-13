import 'dart:async';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/transport/jsonrpc.dart';

void main() {
  test('server.upgrade outlasts the daemon download budget', () async {
    final incoming = StreamController<RpcMessage>();
    final client = E2EClient(incoming.stream, (_) {}, await generateKeyPair());
    expect(client.callTimeoutFor('server.upgrade'), const Duration(minutes: 6));
    for (final m in ['server.restart', 'server.checkUpgrade', 'sessions.list']) {
      expect(client.callTimeoutFor(m), const Duration(seconds: 30));
    }
    await incoming.close();
  });
}
