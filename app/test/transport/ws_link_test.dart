import 'dart:async';
import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/transport/ws_link.dart';

void main() {
  group('WebSocketRpcLink._onData after close', () {
    test(
      'late frame after close does not throw and stream closes cleanly',
      () async {
        final frames = StreamController<dynamic>();
        addTearDown(frames.close);
        final link = WebSocketRpcLink.fromStream(frames.stream);

        // Use a Completer so the test reliably waits for delivery regardless of
        // whether the broadcast controller dispatches synchronously or via microtask.
        final firstDelivered = Completer<String?>();
        link.incoming.listen((m) {
          if (!firstDelivered.isCompleted) firstDelivered.complete(m.method);
        }, onError: firstDelivered.completeError);

        // Feed a frame before close — must be delivered.
        frames.add('${jsonEncode({'jsonrpc': '2.0', 'method': 'ping'})}\n');
        final method = await firstDelivered.future.timeout(
          const Duration(seconds: 5),
        );
        expect(method, 'ping');

        // Close the link (controller is now closed).
        await link.close();

        // Feed a late frame — _onData must guard against the closed controller
        // and NOT throw. Any unhandled async error would bubble up and fail the
        // test, so reaching the end proves the guard works.
        frames.add('${jsonEncode({'jsonrpc': '2.0', 'method': 'late'})}\n');
        await Future.delayed(Duration.zero);
      },
    );
  });

  group('resolveClientUrl', () {
    test('appends /client to a base url', () {
      expect(
        resolveClientUrl('wss://argus.example.ts.net'),
        'wss://argus.example.ts.net/client',
      );
      expect(
        resolveClientUrl('ws://192.168.1.5:8443'),
        'ws://192.168.1.5:8443/client',
      );
    });

    test('treats a bare trailing slash as no path', () {
      expect(resolveClientUrl('wss://host/'), 'wss://host/client');
    });

    test('rejects a non-empty path (route is implicit)', () {
      expect(
        () => resolveClientUrl('wss://host/client'),
        throwsFormatException,
      );
      expect(() => resolveClientUrl('wss://host/x'), throwsFormatException);
    });

    test('rejects bad scheme or missing host', () {
      expect(() => resolveClientUrl('https://host'), throwsFormatException);
      expect(() => resolveClientUrl('wss://'), throwsFormatException);
    });
  });
}
