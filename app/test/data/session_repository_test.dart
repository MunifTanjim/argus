// app/test/data/session_repository_test.dart
import 'dart:async';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/state/control.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/transport/rpc_client.dart';

void main() {
  test('delegates calls to the underlying SessionService', () async {
    final frames = <String>[];
    final incoming = StreamController<RpcMessage>();
    final client = RpcClient(incoming: incoming.stream, sendFrame: frames.add);
    final repo = SessionRepositoryRemote(SessionService(() => client));

    // ignore: unawaited_futures
    repo.respond({'session_id': 's', 'behavior': 'allow'});
    await Future<void>.delayed(Duration.zero);

    expect(frames.single, contains('"method":"sessions.respond"'));
  });

  test('surfaces the service Error when not connected', () async {
    final repo = SessionRepositoryRemote(SessionService(() => null));
    expect(await repo.kill('s'), isA<Error<void>>());
  });
}
