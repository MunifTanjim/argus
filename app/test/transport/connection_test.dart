import 'dart:async';
import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/transport/jsonrpc.dart';

class FakeLink implements RpcLink {
  final _ctrl = StreamController<RpcMessage>.broadcast();
  final List<String> sent = [];
  bool closed = false;

  @override
  Stream<RpcMessage> get incoming => _ctrl.stream;
  @override
  void send(String frame) => sent.add(frame);
  @override
  Future<void> close() async {
    closed = true;
    await _ctrl.close();
  }

  void drop() => _ctrl.addError(StateError('socket dropped'));
}

/// A link that answers every request frame with a null-result response,
/// so keepalive pings succeed and the connection stays healthy.
class PongLink implements RpcLink {
  final _ctrl = StreamController<RpcMessage>.broadcast();
  bool closed = false;

  @override
  Stream<RpcMessage> get incoming => _ctrl.stream;
  @override
  void send(String frame) {
    final m = jsonDecode(frame) as Map<String, dynamic>;
    final id = m['id'];
    if (id != null && !_ctrl.isClosed) {
      _ctrl.add(RpcMessage.fromJson(
          {'jsonrpc': '2.0', 'id': id, 'result': null}));
    }
  }

  @override
  Future<void> close() async {
    closed = true;
    await _ctrl.close();
  }
}

class _Fatal implements FatalConnectError {
  @override
  final String message;
  _Fatal(this.message);
}

void main() {
  test('a fatal connect error stops redialing and surfaces the message',
      () async {
    var attempts = 0;
    final mgr = ConnectionManager(
      connect: () async {
        attempts++;
        throw _Fatal('host key changed — possible MITM');
      },
      baseBackoff: const Duration(milliseconds: 5),
      maxBackoff: const Duration(milliseconds: 20),
    );
    mgr.start();
    await Future<void>.delayed(const Duration(milliseconds: 80));
    expect(mgr.state, ConnState.failed);
    expect(mgr.failureMessage, contains('MITM'));
    // Fatal ⇒ no backoff loop; a single dial, not repeated attempts.
    expect(attempts, 1);
    await mgr.stop();
  });

  test('reaches connected and runs onConnected', () async {
    final link = FakeLink();
    var resynced = false;
    final mgr = ConnectionManager(
      connect: () async => link,
      onConnected: (c) async => resynced = true,
      baseBackoff: const Duration(milliseconds: 5),
      maxBackoff: const Duration(milliseconds: 20),
    );
    final states = <ConnState>[];
    mgr.states.listen(states.add);
    mgr.start();
    await Future<void>.delayed(const Duration(milliseconds: 20));
    expect(mgr.state, ConnState.connected);
    expect(resynced, isTrue);
    expect(states, contains(ConnState.connecting));
    await mgr.stop();
  });

  test('reconnects after a dropped link', () async {
    var attempts = 0;
    final links = <FakeLink>[];
    final mgr = ConnectionManager(
      connect: () async {
        attempts++;
        final l = FakeLink();
        links.add(l);
        return l;
      },
      baseBackoff: const Duration(milliseconds: 5),
      maxBackoff: const Duration(milliseconds: 20),
    );
    mgr.start();
    await Future<void>.delayed(const Duration(milliseconds: 20));
    expect(mgr.state, ConnState.connected);
    links.first.drop();
    await Future<void>.delayed(const Duration(milliseconds: 60));
    expect(attempts, greaterThanOrEqualTo(2));
    expect(mgr.state, ConnState.connected);
    await mgr.stop();
  });

  test('stop prevents further reconnects', () async {
    final mgr = ConnectionManager(
      connect: () async => FakeLink(),
      baseBackoff: const Duration(milliseconds: 5),
      maxBackoff: const Duration(milliseconds: 20),
    );
    mgr.start();
    await Future<void>.delayed(const Duration(milliseconds: 20));
    await mgr.stop();
    expect(mgr.state, ConnState.disconnected);
  });

  test('dial timeout abandons a hung connect and retries', () async {
    var attempt = 0;
    final mgr = ConnectionManager(
      connect: () async {
        attempt++;
        if (attempt == 1) {
          await Completer<RpcLink>().future; // never completes
        }
        return FakeLink();
      },
      dialTimeout: const Duration(milliseconds: 20),
      baseBackoff: const Duration(milliseconds: 5),
      maxBackoff: const Duration(milliseconds: 20),
    );
    mgr.start();
    await Future<void>.delayed(const Duration(milliseconds: 150));
    expect(attempt, greaterThanOrEqualTo(2));
    expect(mgr.state, ConnState.connected);
    await mgr.stop();
  });

  test('onConnected timeout abandons a hung handshake and retries', () async {
    var attempt = 0;
    final mgr = ConnectionManager(
      connect: () async => FakeLink(),
      onConnected: (c) async {
        attempt++;
        if (attempt == 1) await Completer<void>().future; // never completes
      },
      dialTimeout: const Duration(milliseconds: 20),
      baseBackoff: const Duration(milliseconds: 5),
      maxBackoff: const Duration(milliseconds: 20),
    );
    mgr.start();
    await Future<void>.delayed(const Duration(milliseconds: 150));
    expect(attempt, greaterThanOrEqualTo(2));
    expect(mgr.state, ConnState.connected);
    await mgr.stop();
  });

  test('keepalive reconnects when ping goes unanswered', () async {
    var attempts = 0;
    final mgr = ConnectionManager(
      connect: () async {
        attempts++;
        return FakeLink(); // never answers ping
      },
      keepaliveInterval: const Duration(milliseconds: 20),
      keepaliveTimeout: const Duration(milliseconds: 20),
      baseBackoff: const Duration(milliseconds: 5),
      maxBackoff: const Duration(milliseconds: 20),
    );
    mgr.start();
    await Future<void>.delayed(const Duration(milliseconds: 150));
    expect(attempts, greaterThanOrEqualTo(2));
    await mgr.stop();
  });

  test('keepalive stays connected when ping is answered', () async {
    var attempts = 0;
    final mgr = ConnectionManager(
      connect: () async {
        attempts++;
        return PongLink();
      },
      keepaliveInterval: const Duration(milliseconds: 20),
      keepaliveTimeout: const Duration(milliseconds: 20),
      baseBackoff: const Duration(milliseconds: 5),
      maxBackoff: const Duration(milliseconds: 20),
    );
    mgr.start();
    await Future<void>.delayed(const Duration(milliseconds: 150));
    expect(mgr.state, ConnState.connected);
    expect(attempts, 1);
    await mgr.stop();
  });

  test('reconnectNow drops the current link and redials', () async {
    final links = <FakeLink>[];
    final mgr = ConnectionManager(
      connect: () async {
        final l = FakeLink();
        links.add(l);
        return l;
      },
      baseBackoff: const Duration(milliseconds: 5),
      maxBackoff: const Duration(milliseconds: 20),
    );
    mgr.start();
    await Future<void>.delayed(const Duration(milliseconds: 20));
    expect(mgr.state, ConnState.connected);
    mgr.reconnectNow();
    await Future<void>.delayed(const Duration(milliseconds: 20));
    expect(links.length, greaterThanOrEqualTo(2));
    expect(links.first.closed, isTrue);
    expect(mgr.state, ConnState.connected);
    await mgr.stop();
  });
}
