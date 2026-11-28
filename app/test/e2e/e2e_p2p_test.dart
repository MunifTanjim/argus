import 'dart:async';
import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:argus/e2e/e2e.dart';
import 'package:argus/state/gateway.dart' show resumeDirectLinks;
import 'package:argus/transport/connection.dart' show RpcLink;
import 'package:argus/transport/ws_link.dart';
import 'package:flutter_test/flutter_test.dart';

import 'loopback.dart';

Uint8List _json(Object? v) => Uint8List.fromList(utf8.encode(jsonEncode(v)));

const _endpoint = 'ws://node-a.tailnet:7000/p2p';
const _token = 'argus-client-token-xyz';
final _directChan = RegExp(r'^p[0-9a-f]{16}$');

typedef _Path = ({String nodeId, String via});

class _Fleet {
  _Fleet({
    this.gatewayP2P = true,
    this.plaintext = false,
    this.badMac = false,
    this.identifyOpener = false,
    this.reachable = true,
    this.handshakeTimeout = const Duration(seconds: 15),
    this.callTimeout = const Duration(seconds: 30),
  });

  final Duration handshakeTimeout;
  final Duration callTimeout;

  /// Runs in the dialer before the link is returned.
  Future<void> Function()? onDial;

  final bool gatewayP2P;
  final bool plaintext;
  final bool badMac;
  final bool identifyOpener;
  bool reachable;

  late final LoopbackNode node;
  late final MultiNodeLoopbackLink gw;
  late final E2EClient client;
  final paths = <_Path>[];
  final dials = <String>[];
  final directs = <LoopbackDirectLink>[];

  /// Runs inside the node's handler, after the request was recorded.
  void Function(String method)? onRequest;

  /// Runs before connect, so listeners see the first events.
  void Function(E2EClient client)? beforeConnect;

  Future<RpcLink> _dial(String url, Duration timeout) async {
    dials.add(url);
    if (!reachable) throw const SocketException('unreachable');
    await onDial?.call();
    final d = LoopbackDirectLink(
      node,
      tokenHash: plaintext ? hashToken(_token) : null,
      badMac: badMac,
      identifyOpener: identifyOpener,
    );
    directs.add(d);
    return d;
  }

  Future<void> start() async {
    node = LoopbackNode('A', await generateKeyPair(), (m, p) {
      onRequest?.call(m);
      if (m == 'sessions.list') {
        return _json([
          {'id': 's1', 'status': 'idle'},
        ]);
      }
      return _json({'ok': true});
    }, plaintext: plaintext);
    gw = MultiNodeLoopbackLink(
      {'A': node},
      serverInfo: {
        'capabilities': {'p2p': gatewayP2P},
      },
      p2pEndpoints: {
        'A': [_endpoint],
      },
    );
    client = E2EClient(
      gw.incoming,
      gw.send,
      await generateKeyPair(),
      plaintext: plaintext,
      p2pToken: plaintext ? _token : null,
      directDialer: _dial,
      handshakeTimeout: handshakeTimeout,
      callTimeout: callTimeout,
    );
    client.nodePaths.listen(paths.add);
    beforeConnect?.call(client);
    await client.connect();
    await client.channelsSettled;
  }

  Future<void> close() async {
    await client.close();
    await gw.close();
  }
}

Future<void> _until(
  bool Function() done, {
  Duration timeout = const Duration(seconds: 3),
  String reason = '',
}) async {
  final deadline = DateTime.now().add(timeout);
  while (!done()) {
    if (DateTime.now().isAfter(deadline)) fail('timed out: $reason');
    await Future<void>.delayed(const Duration(milliseconds: 10));
  }
}

Future<void> _settle() =>
    Future<void>.delayed(const Duration(milliseconds: 150));

void main() {
  late Duration retry;
  setUp(() {
    retry = E2EClient.p2pRetryInterval;
    E2EClient.p2pRetryInterval = const Duration(hours: 1);
  });
  tearDown(() => E2EClient.p2pRetryInterval = retry);

  test('upgrades to tcp after relay open', () async {
    final f = _Fleet();
    await f.start();
    await _until(() => f.paths.any((p) => p.via == 'tcp'), reason: 'tcp');
    expect(f.paths, [(nodeId: 'A', via: 'gateway'), (nodeId: 'A', via: 'tcp')]);
    expect(f.client.nodeVias, {'A': 'tcp'});
    expect(f.dials, [_endpoint]);

    final hs = f.directs.single.sent
        .map((l) => jsonDecode(l) as Map<String, dynamic>)
        .firstWhere((j) => j['method'] == 'e2e.handshake');
    final chanId = (hs['route'] as Map)['chan_id'] as String;
    expect(chanId, matches(_directChan));

    final list = await f.client.call('sessions.list') as List;
    expect((list.single as Map)['id'], 'A:s1');
    expect(f.node.requests.last.chanId, chanId);

    // The relay channel is closed once its calls settle.
    await _until(
      () => f.gw.relayCloseCalls.contains('chan-0'),
      reason: 'relay close',
    );
    await f.close();
  });

  test('falls back to relay when tcp drops', () async {
    final f = _Fleet();
    await f.start();
    await _until(() => f.paths.any((p) => p.via == 'tcp'), reason: 'tcp');

    final gate = Completer<void>();
    f.node.gate = gate;
    final inflight = f.client.call('host.info', {'node_id': 'A'});
    await _until(
      () => f.node.requests.any((r) => r.method == 'host.info'),
      reason: 'call reached the node',
    );
    final sw = Stopwatch()..start();
    await f.directs.single.drop();
    await expectLater(inflight, throwsA(anything));
    expect(sw.elapsed, lessThan(const Duration(seconds: 2)));
    f.node.gate = null;
    gate.complete();

    await _until(() => f.paths.length == 3, reason: 'fallback path');
    expect(f.paths.last, (nodeId: 'A', via: 'gateway'));
    expect(f.gw.relayOpenCalls, ['A', 'A']);
    expect(f.client.nodeVias, {'A': 'gateway'});
    final list = await f.client.call('sessions.list') as List;
    expect((list.single as Map)['id'], 'A:s1');
    expect(f.node.requests.last.chanId, 'chan-1');
    await f.close();
  });

  test('switch has no offline flicker and no duplicate deltas', () async {
    final f = _Fleet();
    final log = <String>[];
    f.onRequest = (m) {
      final chan = f.node.requests.last.chanId;
      if (m == 'sessions.list' && _directChan.hasMatch(chan)) {
        // Covered by the list: must not be delivered.
        f.node.emitNotification(
          'session.event',
          utf8.encode(
            jsonEncode({
              'type': 'updated',
              'session': {'id': 's1', 'status': 'before-list'},
            }),
          ),
          chanId: chan,
        );
      }
    };
    f.beforeConnect = (client) {
      client.sessionReconciles.listen((r) {
        log.add('reconcile ${r.nodeId}');
        for (final s in r.sessions) {
          expect(s['offline'], isNot(true));
          log.add('  ${s['id']}');
        }
      });
      client.nodePaths.listen((p) => log.add('path ${p.via}'));
      client.aggregatedEvents.listen((e) {
        final s = (e.params as Map)['session'] as Map;
        log.add('event ${s['status']}');
      });
    };
    await f.start();

    await _until(() => log.contains('path tcp'), reason: 'tcp');
    expect(log, ['path gateway', 'reconcile A', '  A:s1', 'path tcp']);

    final direct = f.node.lastChanId!;
    expect(direct, matches(_directChan));
    // The replaced relay link delivers nothing more.
    f.node.emitNotification(
      'session.event',
      utf8.encode(
        jsonEncode({
          'type': 'updated',
          'session': {'id': 's1', 'status': 'old-link'},
        }),
      ),
      chanId: 'chan-0',
    );
    f.node.emitNotification(
      'session.event',
      utf8.encode(
        jsonEncode({
          'type': 'updated',
          'session': {'id': 's1', 'status': 'new-link'},
        }),
      ),
      chanId: direct,
    );
    await _until(() => log.contains('event new-link'), reason: 'new event');
    await _settle();
    expect(log, [
      'path gateway',
      'reconcile A',
      '  A:s1',
      'path tcp',
      'event new-link',
    ]);
    await f.close();
  });

  test('stays on relay when endpoint unreachable', () async {
    final f = _Fleet(reachable: false);
    await f.start();
    await _until(() => f.dials.isNotEmpty, reason: 'dial attempt');
    await _settle();
    expect(f.paths, [(nodeId: 'A', via: 'gateway')]);
    expect(f.client.nodeVias, {'A': 'gateway'});
    final list = await f.client.call('sessions.list') as List;
    expect((list.single as Map)['id'], 'A:s1');
    expect(f.gw.relayCloseCalls, isEmpty);
    await f.close();
  });

  test('no upgrade without gateway p2p capability', () async {
    final f = _Fleet(gatewayP2P: false);
    await f.start();
    await _settle();
    expect(f.dials, isEmpty);
    expect(f.paths, [(nodeId: 'A', via: 'gateway')]);
    await f.close();
  });

  test('plaintext never sends token without valid proof', () async {
    final f = _Fleet(plaintext: true, badMac: true);
    await f.start();
    await _until(
      () => f.directs.isNotEmpty && f.directs.single.closed,
      reason: 'direct link closed',
    );
    await _settle();
    final sent = f.directs.single.sent;
    expect(sent.any((l) => l.contains('token.challenge')), isTrue);
    expect(sent.any((l) => l.contains('token.present')), isFalse);
    expect(sent.any((l) => l.contains(_token)), isFalse);
    expect(f.paths, [(nodeId: 'A', via: 'gateway')]);
    final list = await f.client.call('sessions.list') as List;
    expect((list.single as Map)['id'], 'A:s1');
    await f.close();
  });

  test('plaintext tag and mac match go vector', () {
    final v =
        jsonDecode(
              File('test/e2e/testdata/p2p_admission.json').readAsStringSync(),
            )
            as Map<String, dynamic>;
    final hash = v['token_hash'] as String;
    expect(hashToken(v['token'] as String), hash);
    expect(p2pTag(hash, v['nonce'] as String), v['tag']);
    expect(
      p2pMac(
        hash,
        v['node_id'] as String,
        v['chan_id'] as String,
        v['nonce'] as String,
      ),
      v['mac'],
    );
  });

  test('plaintext never sends the token hash', () async {
    final f = _Fleet(plaintext: true, identifyOpener: true);
    await f.start();
    await _until(() => f.paths.any((p) => p.via == 'tcp'), reason: 'tcp');
    final d = f.directs.single;
    final hash = hashToken(_token);
    expect(d.sent.any((l) => l.contains(hash)), isFalse);

    final msgs = [
      for (final l in d.sent) jsonDecode(l) as Map<String, dynamic>,
    ];
    final chal = msgs.indexWhere((j) => j['method'] == 'token.challenge');
    final present = msgs.indexWhere((j) => j['method'] == 'token.present');
    expect(chal, greaterThanOrEqualTo(0));
    expect(present, greaterThan(chal));
    final cp = msgs[chal]['params'] as Map;
    final nonce = cp['nonce'] as String;
    expect(base64.decode(nonce), hasLength(32));
    expect(base64.encode(base64.decode(nonce)), nonce); // standard, padded
    expect(cp['chan_id'], matches(_directChan));
    expect((msgs[present]['params'] as Map)['token'], _token);
    // Only token.present carries the token.
    expect(
      [
        for (var i = 0; i < d.sent.length; i++)
          if (d.sent[i].contains(_token)) i,
      ],
      [present],
    );
    // The node's node.identify got an immediate method-not-found.
    expect(d.identifyReplies.single.error?.code, -32601);

    final list = await f.client.call('sessions.list') as List;
    expect((list.single as Map)['id'], 'A:s1');
    expect(f.node.requests.last.chanId, cp['chan_id']);
    await f.close();
  });

  test('resume triggers an immediate upgrade attempt', () async {
    final f = _Fleet(reachable: false);
    await f.start();
    await _until(() => f.dials.length == 1, reason: 'first attempt');
    await _settle();
    f.reachable = true;
    resumeDirectLinks(f.client);
    await _until(() => f.paths.any((p) => p.via == 'tcp'), reason: 'tcp');
    expect(f.dials, hasLength(2));
    await f.close();
  });

  test('lose node closes the link', () async {
    final f = _Fleet();
    await f.start();
    await _until(() => f.paths.any((p) => p.via == 'tcp'), reason: 'tcp');
    f.gw.pushNotification('node.event', {
      'type': 'offline',
      'node': {
        'id': 'A',
        'identity_pubkey': base64.encode(f.node.keyPair.publicKey),
        'online': false,
      },
    });
    await _until(() => f.directs.single.closed, reason: 'direct closed');
    await _until(() => f.paths.length == 3, reason: 'lost path');
    expect(f.paths.last, (nodeId: 'A', via: ''));
    expect(f.client.nodeVias, isEmpty);
    await f.close();
  });

  test('gateway loss closes direct links', () async {
    final f = _Fleet();
    await f.start();
    await _until(() => f.paths.any((p) => p.via == 'tcp'), reason: 'tcp');
    await f.gw.close();
    await _until(() => f.directs.single.closed, reason: 'direct closed');
    await _until(() => f.paths.length == 3, reason: 'lost path');
    expect(f.paths.last, (nodeId: 'A', via: ''));
    await f.client.close();
  });

  test('direct dial connects to a ws endpoint', () async {
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => server.close(force: true));
    server.listen((req) async {
      final ws = await WebSocketTransformer.upgrade(req);
      ws.listen((d) => ws.add('{"jsonrpc":"2.0","id":1,"result":"pong"}\n'));
    });
    final link = await WebSocketRpcLink.connectDirect(
      'ws://127.0.0.1:${server.port}/p2p',
    );
    final got = link.incoming.first;
    link.send('{"jsonrpc":"2.0","id":1,"method":"ping"}\n');
    expect((await got).result, 'pong');
    await link.close();
  });

  test('direct dial follows no redirects', () async {
    final target = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => target.close(force: true));
    var reached = false;
    target.listen((req) async {
      reached = true;
      await WebSocketTransformer.upgrade(req);
    });
    final server = await HttpServer.bind(InternetAddress.loopbackIPv4, 0);
    addTearDown(() => server.close(force: true));
    server.listen((req) {
      req.response
        ..statusCode = HttpStatus.found
        ..headers.set(
          HttpHeaders.locationHeader,
          'http://127.0.0.1:${target.port}/p2p',
        );
      req.response.close();
    });
    await expectLater(
      WebSocketRpcLink.connectDirect('ws://127.0.0.1:${server.port}/p2p'),
      throwsA(isA<WebSocketException>()),
    );
    expect(reached, isFalse);
  });

  test('a late reply keeps the channel usable', () async {
    final f = _Fleet(callTimeout: const Duration(milliseconds: 200));
    await f.start();
    await _until(() => f.paths.any((p) => p.via == 'tcp'), reason: 'tcp');
    final gate = Completer<void>();
    f.node.gate = gate;
    await expectLater(
      f.client.call('host.info', {'node_id': 'A'}),
      throwsA(isA<TimeoutException>()),
    );
    f.node.gate = null;
    gate.complete(); // the late reply arrives
    await _settle();
    expect(await f.client.call('host.info', {'node_id': 'A'}), {'ok': true});
    await f.close();
  });

  test('a link that drops during the switch never reports tcp', () async {
    final f = _Fleet();
    final gate = Completer<void>();
    f.beforeConnect = (_) => f.node.gate = gate;
    await f.start();
    await _until(
      () => f.node.requests.any(
        (r) => r.method == 'sessions.list' && _directChan.hasMatch(r.chanId),
      ),
      reason: 'sessions.list on the direct link',
    );
    await f.directs.single.drop();
    f.node.gate = null;
    gate.complete();
    await _until(() => f.paths.length == 2, reason: 'fallback path');
    await _settle();
    expect(f.paths, [
      (nodeId: 'A', via: 'gateway'),
      (nodeId: 'A', via: 'gateway'),
    ]);
    expect(f.client.nodeVias, {'A': 'gateway'});
    await f.close();
  });

  test('a direct link that connects after the deadline is closed', () async {
    final f = _Fleet(handshakeTimeout: const Duration(milliseconds: 300));
    f.onDial = () => Future<void>.delayed(const Duration(milliseconds: 600));
    await f.start();
    await _until(() => f.directs.isNotEmpty, reason: 'late link');
    await _until(() => f.directs.single.closed, reason: 'late link closed');
    expect(f.directs.single.sent, isEmpty);
    expect(f.paths, [(nodeId: 'A', via: 'gateway')]);
    await f.close();
  });

  test('plaintext admission stops when the client closes', () async {
    final f = _Fleet(plaintext: true);
    f.onDial = () => f.client.close();
    await f.start();
    await _until(
      () => f.directs.isNotEmpty && f.directs.single.closed,
      reason: 'link closed',
    );
    expect(f.directs.single.sent, isEmpty);
    await f.gw.close();
  });
}
