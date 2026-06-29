// app/test/state/control_test.dart
import 'dart:async';
import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/transport/rpc_client.dart';
import 'package:argus/state/control.dart';

void main() {
  test('respond calls sessions.respond with params', () async {
    final frames = <String>[];
    final incoming = StreamController<RpcMessage>();
    final client =
        RpcClient(incoming: incoming.stream, sendFrame: frames.add);
    // ignore: unawaited_futures
    SessionService(() => client).respond({'session_id': 's', 'behavior': 'allow'});
    await Future<void>.delayed(Duration.zero);
    expect(frames.single, contains('"method":"sessions.respond"'));
    expect(frames.single, contains('"behavior":"allow"'));
  });

  test('sendInput calls sessions.input with submit+prepare', () async {
    final frames = <String>[];
    final incoming = StreamController<RpcMessage>();
    final client =
        RpcClient(incoming: incoming.stream, sendFrame: frames.add);
    // ignore: unawaited_futures
    SessionService(() => client).sendInput('s', 'hi');
    await Future<void>.delayed(Duration.zero);
    expect(frames.single, contains('"method":"sessions.input"'));
    expect(frames.single, contains('"submit":true'));
    expect(frames.single, contains('"prepare":true'));
  });

  test('null client surfaces Error instead of silently no-op', () async {
    expect(await SessionService(() => null).respond({'x': 1}), isA<Error<void>>());
    expect(await SessionService(() => null).sendInput('s', 'hi'),
        isA<Error<void>>());
  });

  test('sendKeys emits sessions.key with keys', () async {
    final frames = <String>[];
    final incoming = StreamController<RpcMessage>();
    final client =
        RpcClient(incoming: incoming.stream, sendFrame: frames.add);
    // ignore: unawaited_futures
    SessionService(() => client).sendKeys('s', ['Enter', 'Tab']);
    await Future<void>.delayed(Duration.zero);
    expect(frames.single, contains('"method":"sessions.key"'));
    expect(frames.single, contains('"keys":["Enter","Tab"]'));
  });

  test('sendRaw emits sessions.input with submit:false and prepare:false',
      () async {
    final frames = <String>[];
    final incoming = StreamController<RpcMessage>();
    final client =
        RpcClient(incoming: incoming.stream, sendFrame: frames.add);
    // ignore: unawaited_futures
    SessionService(() => client).sendRaw('s', 'hello');
    await Future<void>.delayed(Duration.zero);
    expect(frames.single, contains('"method":"sessions.input"'));
    expect(frames.single, contains('"submit":false'));
    expect(frames.single, contains('"prepare":false'));
  });

  test('capture emits sessions.capture and returns Ok with the fed screen',
      () async {
    final frames = <String>[];
    final incoming = StreamController<RpcMessage>();
    final client =
        RpcClient(incoming: incoming.stream, sendFrame: frames.add);

    String idOf(String frame) {
      final Map<String, dynamic> json =
          // ignore: avoid_dynamic_calls
          (jsonDecode(frame.trim()) as Map<String, dynamic>);
      return json['id'] as String;
    }

    final fut = SessionService(() => client).capture('s');
    await Future<void>.delayed(Duration.zero);
    expect(frames.single, contains('"method":"sessions.capture"'));
    final id = idOf(frames.single);
    incoming.add(RpcMessage.fromJson(
        jsonDecode('{"jsonrpc":"2.0","id":"$id","result":{"screen":"X"}}')));
    final result = await fut;
    expect(result, isA<Ok<String>>());
    expect((result as Ok<String>).value, 'X');
  });

  test('null client: capture returns Error', () async {
    expect(await SessionService(() => null).capture('s'), isA<Error<String>>());
  });

  test('null client: sendKeys returns Error', () async {
    expect(await SessionService(() => null).sendKeys('s', ['Enter']),
        isA<Error<void>>());
  });

  test('null client: sendRaw returns Error', () async {
    expect(
        await SessionService(() => null).sendRaw('s', 'hi'), isA<Error<void>>());
  });

  // --- spawn/kill tests ---

  test('spawn(prompt only) emits sessions.spawn with prompt and no optional keys',
      () async {
    final frames = <String>[];
    final incoming = StreamController<RpcMessage>();
    final client =
        RpcClient(incoming: incoming.stream, sendFrame: frames.add);
    // ignore: unawaited_futures
    SessionService(() => client).spawn(prompt: 'do the thing');
    await Future<void>.delayed(Duration.zero);
    expect(frames.single, contains('"method":"sessions.spawn"'));
    expect(frames.single, contains('"prompt":"do the thing"'));
    expect(frames.single, isNot(contains('"node_id"')));
    expect(frames.single, isNot(contains('"cwd"')));
  });

  test('spawn with all args includes prompt, node_id, cwd', () async {
    final frames = <String>[];
    final incoming = StreamController<RpcMessage>();
    final client =
        RpcClient(incoming: incoming.stream, sendFrame: frames.add);
    // ignore: unawaited_futures
    SessionService(() => client)
        .spawn(nodeId: 'n', cwd: '/p', prompt: 'do the thing');
    await Future<void>.delayed(Duration.zero);
    expect(frames.single, contains('"method":"sessions.spawn"'));
    expect(frames.single, contains('"prompt":"do the thing"'));
    expect(frames.single, contains('"node_id":"n"'));
    expect(frames.single, contains('"cwd":"/p"'));
  });

  test('nodes emits nodes.list and parses node_id/node_label', () async {
    final frames = <String>[];
    final incoming = StreamController<RpcMessage>();
    final client =
        RpcClient(incoming: incoming.stream, sendFrame: frames.add);

    String idOf(String frame) =>
        (jsonDecode(frame.trim()) as Map<String, dynamic>)['id'] as String;

    final fut = SessionService(() => client).nodes();
    await Future<void>.delayed(Duration.zero);
    expect(frames.single, contains('"method":"nodes.list"'));
    final id = idOf(frames.single);
    incoming.add(RpcMessage.fromJson(jsonDecode(
        '{"jsonrpc":"2.0","id":"$id","result":[{"node_id":"n1","node_label":"Box One","capabilities":{"spawn_session":false}},{"node_id":"n2","node_label":"","capabilities":{"spawn_session":true}}]}')));

    final result = await fut;
    final nodes = (result as Ok<List<NodeRef>>).value;
    expect(nodes, hasLength(2));
    expect(nodes[0].id, 'n1');
    expect(nodes[0].label, 'Box One');
    // capabilities.spawn_session is parsed onto the NodeRef.
    expect(nodes[0].spawnSupported, isFalse);
    // Empty label falls back to the id.
    expect(nodes[1].id, 'n2');
    expect(nodes[1].label, 'n2');
    expect(nodes[1].spawnSupported, isTrue);
  });

  test('null client: nodes returns Error', () async {
    expect(await SessionService(() => null).nodes(), isA<Error>());
  });

  test('kill emits sessions.kill with session_id', () async {
    final frames = <String>[];
    final incoming = StreamController<RpcMessage>();
    final client =
        RpcClient(incoming: incoming.stream, sendFrame: frames.add);
    // ignore: unawaited_futures
    SessionService(() => client).kill('s');
    await Future<void>.delayed(Duration.zero);
    expect(frames.single, contains('"method":"sessions.kill"'));
    expect(frames.single, contains('"session_id":"s"'));
  });

  test('null client: spawn returns Error carrying StateError', () async {
    final result = await SessionService(() => null).spawn(prompt: 'x');
    expect(result, isA<Error<void>>());
    expect((result as Error<void>).error, isA<StateError>());
  });

  test('null client: kill returns Error carrying StateError', () async {
    final result = await SessionService(() => null).kill('s');
    expect(result, isA<Error<void>>());
    expect((result as Error<void>).error, isA<StateError>());
  });

  test('reconnect: after swap, respond uses new client not old', () async {
    final framesA = <String>[];
    final framesB = <String>[];
    final incomingA = StreamController<RpcMessage>();
    final incomingB = StreamController<RpcMessage>();
    final clientA =
        RpcClient(incoming: incomingA.stream, sendFrame: framesA.add);
    final clientB =
        RpcClient(incoming: incomingB.stream, sendFrame: framesB.add);

    // Mutable variable simulating the manager's internal _client field.
    RpcClient? current = clientA;
    final control = SessionService(() => current);

    // First call goes through clientA.
    // ignore: unawaited_futures
    control.respond({'session_id': 's', 'behavior': 'allow'});
    await Future<void>.delayed(Duration.zero);
    expect(framesA.length, 1);
    expect(framesB, isEmpty);

    // Simulate reconnect: swap to clientB.
    current = clientB;

    // Second call must go through clientB, not clientA.
    // ignore: unawaited_futures
    control.respond({'session_id': 's', 'behavior': 'deny'});
    await Future<void>.delayed(Duration.zero);
    expect(framesA.length, 1, reason: 'clientA must not receive a second frame');
    expect(framesB.length, 1, reason: 'clientB must receive the frame');
    expect(framesB.single, contains('"method":"sessions.respond"'));
    expect(framesB.single, contains('"behavior":"deny"'));
  });
}
