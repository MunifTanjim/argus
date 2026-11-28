import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:argus/e2e/e2e.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/gateway.dart'
    show NodeSshPrompter, nodeSshDismissCooldown, nodeSshOpener, noteAppResumed;
import 'package:argus/transport/connection.dart' show RpcLink;
import 'package:argus/transport/frame_reader.dart';
import 'package:argus/transport/ssh_bridge.dart';
import 'package:argus/transport/ssh_gateway.dart';
import 'package:argus/transport/ssh_hostkey_store.dart';
import 'package:argus/transport/ssh_key_store.dart';
import 'package:argus/ui/node_ssh_prompter.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

import '../e2e/loopback.dart';

Uint8List _json(Object? v) => Uint8List.fromList(utf8.encode(jsonEncode(v)));

typedef _Path = ({String nodeId, String via});

class _MemKv implements SecureKv {
  final m = <String, String>{};
  @override
  Future<String?> read(String key) async => m[key];
  @override
  Future<void> write(String key, String value) async => m[key] = value;
  @override
  Future<void> delete(String key) async => m.remove(key);
}

/// A node's local socket at the far end of `argus bridge`: plain JSON-RPC,
/// session.event streamed as soon as the client connects.
class _SocketNode implements NodeBridge {
  _SocketNode(this.id, this.identityPub) {
    _send({
      'jsonrpc': '2.0',
      'method': 'session.event',
      'params': {
        'type': 'upsert',
        'session': {'id': 'early'},
      },
    });
  }

  final String id;
  final String identityPub;

  /// Reported by node.identify instead of [id] / [identityPub] when set.
  String? reportId;
  String? reportPub;

  /// When set, requests other than node.identify wait for it.
  Completer<void>? gate;

  final _out = StreamController<Uint8List>();
  final _reader = FrameReader();
  final requests = <({String method, Object? params})>[];

  /// Every line the client wrote.
  final lines = <String>[];
  bool closed = false;

  @override
  Stream<Uint8List> get stream => _out.stream;

  @override
  void add(List<int> data) {
    if (closed) return;
    for (final f in _reader.add(data)) {
      if (f is JsonFrame) unawaited(_handle(f.line));
    }
  }

  Future<void> _handle(String line) async {
    lines.add(line);
    final j = jsonDecode(line) as Map<String, dynamic>;
    final method = j['method'];
    if (method is! String) return;
    requests.add((method: method, params: j['params']));
    final Object? result;
    switch (method) {
      case 'node.identify':
        result = {
          'id': reportId ?? id,
          if ((reportPub ?? identityPub).isNotEmpty)
            'identity_pubkey': reportPub ?? identityPub,
          'capabilities': <String, Object?>{},
        };
      case 'ping':
        result = null;
      case 'sessions.list':
        await gate?.future;
        result = [
          {'id': 's1', 'status': 'idle'},
        ];
      default:
        await gate?.future;
        result = {'ok': true, 'via': 'ssh'};
    }
    _send({'jsonrpc': '2.0', 'id': j['id'], 'result': result});
  }

  void emit(String method, Object? params) =>
      _send({'jsonrpc': '2.0', 'method': method, 'params': params});

  void _send(Map<String, Object?> m) {
    if (closed || _out.isClosed) return;
    _out.add(Uint8List.fromList(utf8.encode('${jsonEncode(m)}\n')));
  }

  /// Ends the link from the far side (ssh exited).
  Future<void> drop() async {
    if (!_out.isClosed) await _out.close();
  }

  @override
  Future<void> close() async {
    closed = true;
    if (!_out.isClosed) await _out.close();
  }
}

class _Prompter implements NodeSshPrompter {
  _Prompter({this.trust = false});

  /// The answer: true trusts, false declines, null gives none.
  bool? trust;
  bool forget = false;

  /// Throws instead of answering when set.
  bool fail = false;

  /// When set, the answer waits for it.
  Completer<void>? gate;
  final asked = <(String, String)>[];
  final changed = <String>[];

  @override
  Future<bool?> confirmHostKey(String hostPort, String fingerprint) async {
    asked.add((hostPort, fingerprint));
    await gate?.future;
    if (fail) throw StateError('no dialog');
    return trust;
  }

  @override
  Future<bool> hostKeyChanged(String hostPort, String message) async {
    changed.add(hostPort);
    return forget;
  }
}

/// Node ids are upper-case letters; node X is reached at host-x.
class _SshFleet {
  _SshFleet({
    this.nodeIds = const ['A'],
    this.plaintext = false,
    this.gatewayP2P = false,
    this.tcp = false,
    Map<String, String>? targets,
    this.command,
    this.labels = const {},
  }) : targets = targets ?? const {'A': 'ssh://me@host-a:2222'};

  final List<String> nodeIds;
  final bool plaintext;
  final bool gatewayP2P;
  final bool tcp;
  final Map<String, String> targets;
  final String? command;
  final Map<String, String> labels;

  late final Map<String, LoopbackNode> nodes;
  late final MultiNodeLoopbackLink gw;
  late final E2EClient client;
  final paths = <_Path>[];
  final events = <NodeEvent>[];
  final opened = <({SshNodeTarget target, String command})>[];
  final bridges = <String, List<_SocketNode>>{};
  final tcpDials = <String>[];

  /// Runs on each new socket node before it is returned.
  void Function(_SocketNode n)? onBridge;

  /// Replaces [open] as the client's opener when set.
  SshOpener? opener;

  String _nodeFor(String host) => host.substring('host-'.length).toUpperCase();

  Future<NodeBridge> open(
    SshNodeTarget t,
    String command,
    Duration timeout,
  ) async {
    opened.add((target: t, command: command));
    final id = _nodeFor(t.host);
    final n = _SocketNode(
      id,
      plaintext ? '' : base64.encode(nodes[id]!.keyPair.publicKey),
    );
    onBridge?.call(n);
    (bridges[id] ??= []).add(n);
    return n;
  }

  final directsA = <LoopbackDirectLink>[];

  Future<RpcLink> _dialTcp(String url, Duration timeout) async {
    tcpDials.add(url);
    final d = LoopbackDirectLink(nodes['A']!);
    directsA.add(d);
    return d;
  }

  Future<void> start() async {
    nodes = {
      for (final id in nodeIds)
        id: LoopbackNode(id, await generateKeyPair(), (m, p) {
          if (m == 'sessions.list') {
            return _json([
              {'id': 's1', 'status': 'idle'},
            ]);
          }
          return _json({'ok': true, 'via': 'gateway'});
        }, plaintext: plaintext),
    };
    gw = MultiNodeLoopbackLink(
      nodes,
      serverInfo: {
        'capabilities': {'p2p': gatewayP2P},
      },
      p2pEndpoints: tcp
          ? {
              'A': ['ws://node-a.tailnet:7000/p2p'],
            }
          : const {},
      labels: labels,
    );
    client = E2EClient(
      gw.incoming,
      gw.send,
      await generateKeyPair(),
      plaintext: plaintext,
      directDialer: tcp ? _dialTcp : null,
      sshOpener: opener ?? open,
      sshTargets: targets,
      sshCommand: command ?? defaultNodeSshCommand,
    );
    client.nodePaths.listen(paths.add);
    client.events.listen(events.add);
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

const _ed = 'ssh-ed25519';

/// Opens like SshBridge.open does up to the exec channel: the host key goes
/// through [NodeHostKeyCheck], then the fleet's socket node is returned.
SshBridgeOpen _hostKeyOpen(_SshFleet f, Map<String, String> fingerprints) =>
    (
      SshNodeTarget t,
      SshKey key,
      HostKeyStore hk, {
      String command = defaultNodeSshCommand,
      required Future<bool> Function(String fingerprint) confirmHostKey,
      Duration timeout = const Duration(seconds: 15),
    }) async {
      final check = NodeHostKeyCheck(hk, t.hostPort, confirmHostKey);
      if (!await check.verify(_ed, fingerprints[t.host]!)) throw check.failure!;
      return f.open(t, command, timeout);
    };

void main() {
  late Duration retry;
  setUp(() {
    retry = E2EClient.p2pRetryInterval;
    E2EClient.p2pRetryInterval = const Duration(hours: 1);
  });
  tearDown(() => E2EClient.p2pRetryInterval = retry);

  test('runs the configured command', () async {
    final f = _SshFleet(command: '/opt/argus/bin/argus bridge');
    await f.start();
    await _until(() => f.paths.any((p) => p.via == 'ssh'), reason: 'ssh');
    final o = f.opened.single;
    expect(o.command, '/opt/argus/bin/argus bridge');
    expect(o.target.host, 'host-a');
    expect(o.target.user, 'me');
    expect(o.target.sshPort, 2222);
    await f.close();

    final d = _SshFleet();
    await d.start();
    await _until(() => d.opened.isNotEmpty, reason: 'ssh dial');
    expect(d.opened.single.command, 'argus bridge');
    await d.close();
  });

  test('first-seen host key asks; declined stays on relay', () async {
    final hk = HostKeyStore(_MemKv());
    final prompter = _Prompter(trust: false);
    final f = _SshFleet();
    f.opener = nodeSshOpener(
      loadKey: () async => const SshKey('pem'),
      hostKeys: hk,
      prompter: () => prompter,
      onTrusted: () => f.client.retryUpgradesNow(),
      open: _hostKeyOpen(f, {'host-a': 'SHA256:aaa'}),
    );
    await f.start();
    await _until(() => prompter.asked.isNotEmpty, reason: 'asked');
    expect(prompter.asked.single, ('host-a:2222', 'SHA256:aaa'));
    await _settle();
    expect(f.paths, [(nodeId: 'A', via: 'gateway')]);
    expect(await hk.pinned('host-a:2222', _ed), isNull);
    expect(f.bridges, isEmpty);

    // A declined key is not asked again.
    f.client.retryUpgradesNow();
    await _settle();
    expect(prompter.asked, hasLength(1));
    expect(f.client.nodeVias, {'A': 'gateway'});
    await f.close();

    // Trusted: pinned, and the node moves to ssh without waiting for a retry.
    final hk2 = HostKeyStore(_MemKv());
    final yes = _Prompter(trust: true);
    final g = _SshFleet();
    g.opener = nodeSshOpener(
      loadKey: () async => const SshKey('pem'),
      hostKeys: hk2,
      prompter: () => yes,
      onTrusted: () => g.client.retryUpgradesNow(),
      open: _hostKeyOpen(g, {'host-a': 'SHA256:aaa'}),
    );
    await g.start();
    await _until(() => g.paths.any((p) => p.via == 'ssh'), reason: 'ssh');
    expect(yes.asked, hasLength(1));
    expect(await hk2.pinned('host-a:2222', _ed), 'SHA256:aaa');
    await g.close();
  });

  test('trusting a first-seen key moves a node from tcp to ssh', () async {
    final hk = HostKeyStore(_MemKv());
    final prompter = _Prompter(trust: true)..gate = Completer<void>();
    final f = _SshFleet(gatewayP2P: true, tcp: true);
    f.opener = nodeSshOpener(
      loadKey: () async => const SshKey('pem'),
      hostKeys: hk,
      prompter: () => prompter,
      onTrusted: () => f.client.retryUpgradesNow(),
      open: _hostKeyOpen(f, {'host-a': 'SHA256:aaa'}),
    );
    await f.start();
    await _until(() => f.paths.any((p) => p.via == 'tcp'), reason: 'tcp');
    expect(prompter.asked, hasLength(1));
    final tcp = f.directsA.single;

    prompter.gate!.complete();
    await _until(() => f.paths.last.via == 'ssh', reason: 'ssh');
    expect(f.paths, [
      (nodeId: 'A', via: 'gateway'),
      (nodeId: 'A', via: 'tcp'),
      (nodeId: 'A', via: 'ssh'),
    ]);
    expect(await hk.pinned('host-a:2222', _ed), 'SHA256:aaa');
    await _until(() => tcp.closed, reason: 'tcp link retired');
    final r = await f.client.call('host.info', {'node_id': 'A'});
    expect((r as Map)['via'], 'ssh');
    await f.close();
  });

  test(
    'a dismissed host-key prompt waits for the cooldown or a resume',
    () async {
      final hk = HostKeyStore(_MemKv());
      final prompter = _Prompter(trust: null); // dismisses: no answer
      var clock = DateTime(2026);
      final f = _SshFleet();
      f.opener = nodeSshOpener(
        loadKey: () async => const SshKey('pem'),
        hostKeys: hk,
        prompter: () => prompter,
        onTrusted: () => f.client.retryUpgradesNow(),
        open: _hostKeyOpen(f, {'host-a': 'SHA256:aaa'}),
        now: () => clock,
      );
      await f.start();
      await _until(() => prompter.asked.isNotEmpty, reason: 'asked');
      await _settle();

      // Dismissed: later dials do not ask again within the cooldown.
      f.client.retryUpgradesNow();
      await _settle();
      expect(prompter.asked, hasLength(1));

      // Past the cooldown it asks again.
      clock = clock.add(nodeSshDismissCooldown);
      f.client.retryUpgradesNow();
      await _until(() => prompter.asked.length == 2, reason: 'after cooldown');
      await _settle();
      f.client.retryUpgradesNow();
      await _settle();
      expect(prompter.asked, hasLength(2));

      // An app resume ends the cooldown at once.
      noteAppResumed();
      prompter.trust = true;
      f.client.retryUpgradesNow();
      await _until(() => f.paths.any((p) => p.via == 'ssh'), reason: 'resumed');
      expect(prompter.asked, hasLength(3));
      await f.close();
    },
  );

  test('a host-key prompt that could not be shown is asked again', () async {
    for (final (name, configure) in <(String, void Function(_Prompter))>[
      ('prompt failed', (p) => p.fail = true),
    ]) {
      final hk = HostKeyStore(_MemKv());
      final prompter = _Prompter();
      configure(prompter);
      final f = _SshFleet();
      f.opener = nodeSshOpener(
        loadKey: () async => const SshKey('pem'),
        hostKeys: hk,
        prompter: () => prompter,
        onTrusted: () => f.client.retryUpgradesNow(),
        open: _hostKeyOpen(f, {'host-a': 'SHA256:aaa'}),
      );
      await f.start();
      await _until(() => prompter.asked.isNotEmpty, reason: '$name: asked');
      await _settle();
      expect(f.client.nodeVias, {'A': 'gateway'}, reason: name);

      prompter
        ..fail = false
        ..trust = true;
      f.client.retryUpgradesNow();
      await _until(
        () => f.paths.any((p) => p.via == 'ssh'),
        reason: '$name: asked again and trusted',
      );
      expect(prompter.asked, hasLength(2), reason: name);
      await f.close();
    }

    // No prompter (no UI yet): nothing remembered, asked once one exists.
    final hk = HostKeyStore(_MemKv());
    _Prompter? prompter;
    final f = _SshFleet();
    f.opener = nodeSshOpener(
      loadKey: () async => const SshKey('pem'),
      hostKeys: hk,
      prompter: () => prompter,
      onTrusted: () => f.client.retryUpgradesNow(),
      open: _hostKeyOpen(f, {'host-a': 'SHA256:aaa'}),
    );
    await f.start();
    await _until(() => f.opened.isEmpty && f.client.nodeVias.isNotEmpty);
    await _settle();
    expect(f.client.nodeVias, {'A': 'gateway'});
    prompter = _Prompter(trust: true);
    f.client.retryUpgradesNow();
    await _until(() => f.paths.any((p) => p.via == 'ssh'), reason: 'no UI');
    expect(prompter.asked, hasLength(1));
    await f.close();
  });

  testWidgets('the dialog prompter cannot ask without a navigator', (
    tester,
  ) async {
    final p = DialogNodeSshPrompter(GlobalKey<NavigatorState>());
    await expectLater(
      p.confirmHostKey('h:22', 'SHA256:x'),
      throwsA(isA<StateError>()),
    );
  });

  testWidgets('dismissing the trust dialog gives no answer', (tester) async {
    final key = GlobalKey<NavigatorState>();
    await tester.pumpWidget(
      MaterialApp(navigatorKey: key, home: const SizedBox()),
    );
    final p = DialogNodeSshPrompter(key);
    bool? answer = true;
    var done = false;
    unawaited(
      p.confirmHostKey('h:22', 'SHA256:x').then((a) {
        answer = a;
        done = true;
      }),
    );
    await tester.pumpAndSettle();
    expect(find.text('Trust SSH host key?'), findsOneWidget);
    await tester.tapAt(const Offset(5, 5)); // the barrier
    await tester.pumpAndSettle();
    expect(done, isTrue);
    expect(answer, isNull);

    unawaited(p.confirmHostKey('h:22', 'SHA256:x').then((a) => answer = a));
    await tester.pumpAndSettle();
    await tester.tap(find.byKey(const Key('node-ssh-decline')));
    await tester.pumpAndSettle();
    expect(answer, isFalse);
  });

  test('changed host key fails that node only', () async {
    final hk = HostKeyStore(_MemKv());
    await hk.pin('host-a:22', _ed, 'SHA256:old');
    await hk.pin('host-b:22', _ed, 'SHA256:bbb');
    final prompter = _Prompter();
    final f = _SshFleet(
      nodeIds: ['A', 'B'],
      targets: {'A': 'ssh://host-a', 'B': 'ssh://host-b'},
    );
    f.opener = nodeSshOpener(
      loadKey: () async => const SshKey('pem'),
      hostKeys: hk,
      prompter: () => prompter,
      onTrusted: () => f.client.retryUpgradesNow(),
      open: _hostKeyOpen(f, {'host-a': 'SHA256:new', 'host-b': 'SHA256:bbb'}),
    );
    await f.start();
    await _until(
      () => f.paths.contains((nodeId: 'B', via: 'ssh')),
      reason: 'B on ssh',
    );
    await _until(() => prompter.changed.isNotEmpty, reason: 'surfaced');
    expect(prompter.changed, ['host-a:22']);
    expect(prompter.asked, isEmpty);
    expect(f.client.nodeVias, {'A': 'gateway', 'B': 'ssh'});
    expect(await hk.pinned('host-a:22', _ed), 'SHA256:old');

    // Surfaced once, not on every retry.
    f.client.retryUpgradesNow();
    await _settle();
    expect(prompter.changed, hasLength(1));
    await f.close();
  });

  test('ssh preferred over tcp', () async {
    final f = _SshFleet(gatewayP2P: true, tcp: true);
    await f.start();
    await _until(() => f.paths.any((p) => p.via == 'ssh'), reason: 'ssh');
    await _settle();
    expect(f.paths, [(nodeId: 'A', via: 'gateway'), (nodeId: 'A', via: 'ssh')]);
    expect(f.tcpDials, isEmpty);
    expect(f.client.nodeVias, {'A': 'ssh'});

    final sock = f.bridges['A']!.single;
    // Direct frames: params, no route, no body.
    final identify = jsonDecode(sock.lines.first) as Map<String, dynamic>;
    expect(identify['method'], 'node.identify');
    expect(identify.containsKey('route'), isFalse);
    expect(identify.containsKey('body'), isFalse);

    final list = await f.client.call('sessions.list') as List;
    expect((list.single as Map)['id'], 'A:s1');
    final r = await f.client.call('host.info', {'node_id': 'A'});
    expect((r as Map)['via'], 'ssh');
    expect((jsonDecode(sock.lines.last) as Map)['params'], {'node_id': 'A'});

    // The event sent before the switch never reached the client; later ones
    // do, like relay notifications.
    sock.emit('session.event', {
      'type': 'upsert',
      'session': {'id': 'late'},
    });
    await _until(() => f.events.isNotEmpty, reason: 'event');
    expect(f.events.single.nodeId, 'A');
    expect(
      jsonDecode(utf8.decode(f.events.single.params)),
      containsPair('session', {'id': 'late'}),
    );
    await f.close();
    expect(sock.closed, isTrue);
  });

  test('identity mismatch stays on relay', () async {
    final f = _SshFleet(gatewayP2P: true, tcp: true);
    f.onBridge = (n) => n.reportId = 'B';
    await f.start();
    await _until(() => f.bridges.isNotEmpty, reason: 'ssh dial');
    // Falls through to tcp, never to ssh.
    await _until(() => f.paths.any((p) => p.via == 'tcp'), reason: 'tcp');
    expect(f.paths.where((p) => p.via == 'ssh'), isEmpty);
    expect(f.bridges['A']!.single.closed, isTrue);
    await f.close();

    final g = _SshFleet();
    g.onBridge = (n) => n.reportPub = base64.encode(Uint8List(32));
    await g.start();
    await _until(() => g.bridges.isNotEmpty, reason: 'ssh dial');
    await _settle();
    expect(g.paths, [(nodeId: 'A', via: 'gateway')]);
    expect(g.bridges['A']!.single.closed, isTrue);
    await g.close();
  });

  test('works in plaintext profile', () async {
    final f = _SshFleet(plaintext: true);
    await f.start();
    await _until(() => f.paths.any((p) => p.via == 'ssh'), reason: 'ssh');
    final list = await f.client.call('sessions.list') as List;
    expect((list.single as Map)['id'], 'A:s1');
    await f.close();
  });

  test('rejects dash-prefixed user or host', () async {
    expect(
      () => parseSshNodeTarget('ssh://-oProxyCommand=x@host'),
      throwsFormatException,
    );
    expect(() => parseSshNodeTarget('ssh://-host'), throwsFormatException);
    expect(
      () => parseSshGatewayUrl('ssh://-oProxyCommand=x@host'),
      throwsFormatException,
    );
    expect(() => parseSshGatewayUrl('ssh://-host'), throwsFormatException);
    expect(() => parseSshNodeTarget('ssh://host/x'), throwsFormatException);
    expect(() => parseSshNodeTarget('ssh://host?x=1'), throwsFormatException);
    expect(() => parseSshNodeTarget('https://host'), throwsFormatException);
    final t = parseSshNodeTarget('ssh://me@host:2222');
    expect((t.user, t.host, t.sshPort), ('me', 'host', 2222));
    expect(parseSshNodeTarget('ssh://host').hostPort, 'host:22');

    // Such a target is never dialed.
    final f = _SshFleet(targets: {'A': 'ssh://-oProxyCommand=x@host-a'});
    await f.start();
    await _settle();
    expect(f.opened, isEmpty);
    expect(f.client.nodeVias, {'A': 'gateway'});
    await f.close();
  });

  test('ssh drop falls back to relay at once', () async {
    final f = _SshFleet();
    await f.start();
    await _until(() => f.paths.any((p) => p.via == 'ssh'), reason: 'ssh');
    final sock = f.bridges['A']!.single;
    sock.gate = Completer<void>();
    final inflight = f.client.call('host.info', {'node_id': 'A'});
    await _until(
      () => sock.requests.any((r) => r.method == 'host.info'),
      reason: 'call reached the node',
    );
    final sw = Stopwatch()..start();
    await sock.drop();
    await expectLater(inflight, throwsA(anything));
    expect(sw.elapsed, lessThan(const Duration(seconds: 2)));
    await _until(() => f.paths.length == 3, reason: 'fallback');
    expect(f.paths.last, (nodeId: 'A', via: 'gateway'));
    expect(sock.closed, isTrue);
    final r = await f.client.call('host.info', {'node_id': 'A'});
    expect((r as Map)['via'], 'gateway');
    await f.close();
  });

  test('ssh link closes when the node leaves', () async {
    final f = _SshFleet();
    await f.start();
    await _until(() => f.paths.any((p) => p.via == 'ssh'), reason: 'ssh');
    f.gw.pushNotification('node.event', {
      'type': 'offline',
      'node': {'id': 'A'},
    });
    await _until(() => f.paths.last.via == '', reason: 'lost');
    expect(f.bridges['A']!.single.closed, isTrue);
    await f.close();
  });

  test(
    'targets match id or label case-insensitively; shared label skipped',
    () async {
      final f = _SshFleet(targets: {'a-BOX': 'ssh://host-a'});
      await f.start();
      await _until(() => f.paths.any((p) => p.via == 'ssh'), reason: 'label');
      await f.close();

      final g = _SshFleet(targets: {'a': 'ssh://host-a'});
      await g.start();
      await _until(() => g.paths.any((p) => p.via == 'ssh'), reason: 'id');
      await g.close();

      final h = _SshFleet(
        nodeIds: ['A', 'B'],
        targets: {'Box': 'ssh://host-a'},
        labels: {'A': 'box', 'B': 'BOX'},
      );
      await h.start();
      await _settle();
      expect(h.opened, isEmpty);
      expect(h.client.nodeVias, {'A': 'gateway', 'B': 'gateway'});
      await h.close();
    },
  );
}
