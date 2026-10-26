import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:cbor/simple.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/transport/frame_reader.dart';
import 'package:argus/transport/jsonrpc.dart';
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

        frames.add('${jsonEncode({'jsonrpc': '2.0', 'method': 'ping'})}\n');
        final method = await firstDelivered.future.timeout(
          const Duration(seconds: 5),
        );
        expect(method, 'ping');

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

  test('ws link decodes a cbor frame and a json line in one message', () async {
    final frames = StreamController<dynamic>();
    addTearDown(frames.close);
    final link = WebSocketRpcLink.fromStream(frames.stream);
    final got = <RpcMessage>[];
    final two = Completer<void>();
    link.incoming.listen((m) {
      got.add(m);
      if (got.length == 2) two.complete();
    });
    final env = Uint8List.fromList(
      cbor.encode({
        'jsonrpc': '2.0',
        'id': 7,
        'route': {'chan_id': 'c1'},
        'body': Uint8List.fromList([1, 2, 3]),
      }),
    );
    frames.add(
      Uint8List.fromList([
        ...frameCbor(env),
        ...utf8.encode('${jsonEncode({'jsonrpc': '2.0', 'method': 'ping'})}\n'),
      ]),
    );
    await two.future.timeout(const Duration(seconds: 5));
    expect(got[0].cbor, isTrue);
    expect(got[0].id, '7');
    expect((got[0].route as Map)['chan_id'], 'c1');
    expect(got[0].body, [1, 2, 3]);
    expect(got[1].cbor, isFalse);
    expect(got[1].method, 'ping');
    await link.close();
  });

  test('ws link errors and closes on a malformed cbor frame', () async {
    final frames = StreamController<dynamic>();
    addTearDown(frames.close);
    var closes = 0;
    final link = WebSocketRpcLink.fromStream(
      frames.stream,
      closeHook: () async => closes++,
    );
    final got = <RpcMessage>[];
    final errors = <Object>[];
    final done = Completer<void>();
    link.incoming.listen(got.add, onError: errors.add, onDone: done.complete);
    final ping = utf8.encode(
      '${jsonEncode({'jsonrpc': '2.0', 'method': 'ping'})}\n',
    );
    frames.add(
      Uint8List.fromList([
        ...frameCbor(Uint8List.fromList(cbor.encode(5))),
        ...frameCbor(Uint8List.fromList([0xa1])),
        ...ping,
      ]),
    );
    frames.add(Uint8List.fromList(ping));
    await done.future.timeout(const Duration(seconds: 5));
    expect(errors, hasLength(1));
    expect(got, isEmpty);
    expect(closes, greaterThanOrEqualTo(1));
  });
}
