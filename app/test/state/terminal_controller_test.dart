import 'dart:async';
import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/enums.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/transport/rpc_client.dart';
import 'package:argus/state/terminal_controller.dart';

void main() {
  late StreamController<RpcMessage> incoming;
  late List<String> sent;
  late RpcClient client;

  setUp(() {
    incoming = StreamController<RpcMessage>.broadcast();
    sent = <String>[];
    client = RpcClient(incoming: incoming.stream, sendFrame: sent.add);
  });
  tearDown(() => client.close());

  Map<String, dynamic> lastReq() =>
      jsonDecode(sent.last.trim()) as Map<String, dynamic>;

  TerminalAttach start({
    void Function(List<int>)? onData,
    void Function(TerminalExitReason)? onExited,
  }) {
    final a = TerminalAttach(
      client: client,
      sessionId: 'sess',
      cols: 80,
      rows: 24,
      onData: onData ?? (_) {},
      onExited: onExited,
    )..start();
    return a;
  }

  test('start sends terminal.open with term id and dims', () {
    final a = start();
    final req = lastReq();
    expect(req['method'], 'terminal.open');
    final p = req['params'] as Map;
    expect(p['term_id'], a.termId);
    expect(p['session_id'], 'sess');
    expect(p['cols'], 80);
    expect(p['rows'], 24);
    expect(a.termId, isNotEmpty);
  });

  test('terminal.output for this term is decoded to onData', () async {
    final got = <List<int>>[];
    final a = start(onData: got.add);
    final data = base64Encode(utf8.encode('hi'));
    incoming.add(RpcMessage.fromJson(jsonDecode(
        '{"jsonrpc":"2.0","method":"terminal.output","params":{"term_id":"${a.termId}","data":"$data"}}')));
    await Future<void>.delayed(Duration.zero);
    expect(got.single, utf8.encode('hi'));
  });

  test('terminal.output for another term is ignored', () async {
    final got = <List<int>>[];
    start(onData: got.add);
    final data = base64Encode(utf8.encode('hi'));
    incoming.add(RpcMessage.fromJson(jsonDecode(
        '{"jsonrpc":"2.0","method":"terminal.output","params":{"term_id":"other","data":"$data"}}')));
    await Future<void>.delayed(Duration.zero);
    expect(got, isEmpty);
  });

  test('terminal.exited without reason fires onExited with exited', () async {
    TerminalExitReason? reason;
    final a = start(onExited: (r) => reason = r);
    incoming.add(RpcMessage.fromJson(jsonDecode(
        '{"jsonrpc":"2.0","method":"terminal.exited","params":{"term_id":"${a.termId}"}}')));
    await Future<void>.delayed(Duration.zero);
    expect(reason, TerminalExitReason.exited);
  });

  test('terminal.exited with reason=evicted fires onExited with evicted',
      () async {
    TerminalExitReason? reason;
    final a = start(onExited: (r) => reason = r);
    incoming.add(RpcMessage.fromJson(jsonDecode(
        '{"jsonrpc":"2.0","method":"terminal.exited","params":{"term_id":"${a.termId}","reason":"evicted"}}')));
    await Future<void>.delayed(Duration.zero);
    expect(reason, TerminalExitReason.evicted);
  });

  test('send emits terminal.input with base64 data', () {
    final a = start();
    a.send([3]); // ctrl-c
    final req = lastReq();
    expect(req['method'], 'terminal.input');
    final p = req['params'] as Map;
    expect(p['term_id'], a.termId);
    expect(p['data'], base64Encode([3]));
  });

  test('resize emits terminal.resize and dedupes unchanged dims', () {
    final a = start();
    final before = sent.length;
    a.resize(80, 24);
    expect(sent.length, before);
    a.resize(100, 40);
    final req = lastReq();
    expect(req['method'], 'terminal.resize');
    final p = req['params'] as Map;
    expect(p['cols'], 100);
    expect(p['rows'], 40);
  });

  test('open failure fires onError and stops listening', () async {
    Object? err;
    final got = <List<int>>[];
    final a = TerminalAttach(
      client: client,
      sessionId: 'sess',
      cols: 80,
      rows: 24,
      onData: got.add,
      onError: (e) => err = e,
    )..start();

    // Fail the terminal.open with an error response for its request id.
    final openId = lastReq()['id'];
    incoming.add(RpcMessage.fromJson(jsonDecode(
        '{"jsonrpc":"2.0","id":"$openId","error":{"code":-32600,"message":"boom"}}')));
    await Future<void>.delayed(Duration.zero);
    expect(err, isNotNull, reason: 'onError should fire on open failure');

    // After a failed open the attach must stop listening: a late output is ignored.
    final data = base64Encode(utf8.encode('hi'));
    incoming.add(RpcMessage.fromJson(jsonDecode(
        '{"jsonrpc":"2.0","method":"terminal.output","params":{"term_id":"${a.termId}","data":"$data"}}')));
    await Future<void>.delayed(Duration.zero);
    expect(got, isEmpty,
        reason: 'subscription must be cancelled after open failure');
  });

  test('dispose emits terminal.close', () {
    final a = start();
    a.dispose();
    final req = lastReq();
    expect(req['method'], 'terminal.close');
    expect((req['params'] as Map)['term_id'], a.termId);
  });

  test('output after dispose is ignored', () async {
    final got = <List<int>>[];
    final a = start(onData: got.add);
    a.dispose();
    final data = base64Encode(utf8.encode('late'));
    incoming.add(RpcMessage.fromJson(jsonDecode(
        '{"jsonrpc":"2.0","method":"terminal.output","params":{"term_id":"${a.termId}","data":"$data"}}')));
    await Future<void>.delayed(Duration.zero);
    expect(got, isEmpty, reason: 'a disposed attach must stop delivering output');
  });

  test('send with empty data is a no-op', () {
    final a = start();
    final before = sent.length;
    a.send(const []);
    expect(sent.length, before, reason: 'empty input must not emit a frame');
  });

  test('send after dispose is a no-op', () {
    final a = start();
    a.dispose();
    final afterClose = sent.length;
    a.send([3]);
    expect(sent.length, afterClose, reason: 'a disposed attach must not send');
  });

  test('resize with non-positive dims is a no-op', () {
    final a = start();
    final before = sent.length;
    a.resize(0, 24);
    a.resize(80, 0);
    a.resize(-1, -1);
    expect(sent.length, before, reason: 'non-positive resize must not emit a frame');
  });

  test('terminal.exited stops listening and dispose then sends no close', () async {
    final got = <List<int>>[];
    final a = start(onData: got.add);
    incoming.add(RpcMessage.fromJson(jsonDecode(
        '{"jsonrpc":"2.0","method":"terminal.exited","params":{"term_id":"${a.termId}"}}')));
    await Future<void>.delayed(Duration.zero);

    // The node already closed the term, so the attach stops listening itself: a
    // late output is dropped even without an explicit dispose() from the UI.
    final data = base64Encode(utf8.encode('late'));
    incoming.add(RpcMessage.fromJson(jsonDecode(
        '{"jsonrpc":"2.0","method":"terminal.output","params":{"term_id":"${a.termId}","data":"$data"}}')));
    await Future<void>.delayed(Duration.zero);
    expect(got, isEmpty, reason: 'exit must stop delivering output');

    // A follow-up dispose (e.g. from the route pop) must not emit terminal.close
    // for the already-gone term.
    final before = sent.length;
    a.dispose();
    expect(sent.length, before, reason: 'no terminal.close after the term already exited');
  });
}
