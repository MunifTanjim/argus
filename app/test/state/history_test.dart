// app/test/state/history_test.dart
import 'dart:async';
import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/models/history.dart';
import 'package:argus/transport/jsonrpc.dart';
import 'package:argus/transport/rpc_client.dart';
import 'package:argus/state/history.dart';

void main() {
  // Helper: extract the id from a captured frame string.
  String idOf(String frame) =>
      (jsonDecode(frame.trim()) as Map<String, dynamic>)['id'] as String;

  group('HistoryApi frame-capture', () {
    test('projects() emits sessions.historyProjects with no params', () async {
      final frames = <String>[];
      final incoming = StreamController<RpcMessage>();
      final client =
          RpcClient(incoming: incoming.stream, sendFrame: frames.add);
      // ignore: unawaited_futures
      HistoryApi(() => client).projects();
      await Future<void>.delayed(Duration.zero);
      expect(frames.single, contains('"method":"sessions.historyProjects"'));
      // no params key at all (or empty params object)
      final decoded =
          jsonDecode(frames.single.trim()) as Map<String, dynamic>;
      expect(decoded.containsKey('params'), isFalse);
    });

    test('sessions() emits sessions.historySessions with project_dir/limit/offset', () async {
      final frames = <String>[];
      final incoming = StreamController<RpcMessage>();
      final client =
          RpcClient(incoming: incoming.stream, sendFrame: frames.add);
      // ignore: unawaited_futures
      HistoryApi(() => client).sessions(
        nodeId: 'node-1',
        projectDir: '/my/proj',
        limit: 100,
        offset: 0,
      );
      await Future<void>.delayed(Duration.zero);
      expect(frames.single, contains('"method":"sessions.historySessions"'));
      expect(frames.single, contains('"node_id":"node-1"'));
      expect(frames.single, contains('"project_dir":"/my/proj"'));
      expect(frames.single, contains('"limit":100'));
      expect(frames.single, contains('"offset":0'));
    });

    test('sessions() includes node_id when nodeId is non-empty', () async {
      final frames = <String>[];
      final incoming = StreamController<RpcMessage>();
      final client =
          RpcClient(incoming: incoming.stream, sendFrame: frames.add);
      // ignore: unawaited_futures
      HistoryApi(() => client).sessions(
        nodeId: 'node-42',
        projectDir: '/my/proj',
        limit: 100,
        offset: 50,
      );
      await Future<void>.delayed(Duration.zero);
      expect(frames.single, contains('"node_id":"node-42"'));
    });

    test('sessions() errors and sends nothing when nodeId is missing', () async {
      final frames = <String>[];
      final incoming = StreamController<RpcMessage>();
      final client =
          RpcClient(incoming: incoming.stream, sendFrame: frames.add);
      final result = await HistoryApi(() => client).sessions(
        nodeId: '',
        projectDir: '/my/proj',
        limit: 100,
        offset: 0,
      );
      expect(result, isA<Error<HistorySessionPage>>());
      expect(frames, isEmpty);
    });

    test('transcript() errors and sends nothing when nodeId is missing', () async {
      final frames = <String>[];
      final incoming = StreamController<RpcMessage>();
      final client =
          RpcClient(incoming: incoming.stream, sendFrame: frames.add);
      final result = await HistoryApi(() => client)
          .transcript(transcriptPath: '/t.jsonl');
      expect(result, isA<Error<List<Chunk>>>());
      expect(frames, isEmpty);
    });

    test('transcript() emits sessions.historyTranscript with transcript_path', () async {
      final frames = <String>[];
      final incoming = StreamController<RpcMessage>();
      final client =
          RpcClient(incoming: incoming.stream, sendFrame: frames.add);
      // ignore: unawaited_futures
      HistoryApi(() => client).transcript(
        nodeId: 'node-1',
        transcriptPath: '/path/to/transcript.jsonl',
      );
      await Future<void>.delayed(Duration.zero);
      expect(frames.single,
          contains('"method":"sessions.historyTranscript"'));
      expect(frames.single,
          contains('"transcript_path":"/path/to/transcript.jsonl"'));
      expect(frames.single, contains('"node_id":"node-1"'));
    });

    test('transcript() includes node_id when nodeId is non-empty', () async {
      final frames = <String>[];
      final incoming = StreamController<RpcMessage>();
      final client =
          RpcClient(incoming: incoming.stream, sendFrame: frames.add);
      // ignore: unawaited_futures
      HistoryApi(() => client).transcript(
        nodeId: 'node-7',
        transcriptPath: '/path/to/transcript.jsonl',
      );
      await Future<void>.delayed(Duration.zero);
      expect(frames.single, contains('"node_id":"node-7"'));
    });
  });

  group('HistoryApi parse tests', () {
    test('projects() returns Ok with parsed HistoryProject list', () async {
      final incoming = StreamController<RpcMessage>();
      final sent = <String>[];
      final client =
          RpcClient(incoming: incoming.stream, sendFrame: sent.add);

      final fut = HistoryApi(() => client).projects();

      // Feed matching response.
      await Future<void>.delayed(Duration.zero);
      final id = idOf(sent.single);
      incoming.add(RpcMessage.fromJson(jsonDecode(
          '{"jsonrpc":"2.0","id":"$id","result":[{"project_dir":"/proj","cwd":"/proj","label":"My Project","session_count":3,"last_activity":"2026-01-01T00:00:00Z"}]}')));

      final result = await fut;
      final projects = (result as Ok<List<HistoryProject>>).value;
      expect(projects, hasLength(1));
      expect(projects.first.projectDir, '/proj');
      expect(projects.first.label, 'My Project');
      expect(projects.first.sessionCount, 3);
    });

    test('transcript() returns Ok with parsed Chunk list', () async {
      final incoming = StreamController<RpcMessage>();
      final sent = <String>[];
      final client =
          RpcClient(incoming: incoming.stream, sendFrame: sent.add);

      final fut = HistoryApi(() => client)
          .transcript(nodeId: 'node-1', transcriptPath: '/t.jsonl');

      await Future<void>.delayed(Duration.zero);
      final id = idOf(sent.single);
      incoming.add(RpcMessage.fromJson(jsonDecode(
          '{"jsonrpc":"2.0","id":"$id","result":{"chunks":[{"id":"c1","kind":"user","text":"Hello"}]}}')));

      final result = await fut;
      final chunks = (result as Ok<List<Chunk>>).value;
      expect(chunks, hasLength(1));
      expect(chunks.first.id, 'c1');
      expect(chunks.first.text, 'Hello');
    });

    test('transcript() returns Ok with empty list when chunks is empty',
        () async {
      final incoming = StreamController<RpcMessage>();
      final sent = <String>[];
      final client =
          RpcClient(incoming: incoming.stream, sendFrame: sent.add);

      final fut = HistoryApi(() => client)
          .transcript(nodeId: 'node-1', transcriptPath: '/t.jsonl');

      await Future<void>.delayed(Duration.zero);
      final id = idOf(sent.single);
      incoming.add(RpcMessage.fromJson(jsonDecode(
          '{"jsonrpc":"2.0","id":"$id","result":{"chunks":[]}}')));

      final result = await fut;
      expect((result as Ok<List<Chunk>>).value, isEmpty);
    });
  });

  group('HistoryApi null-client', () {
    test('projects() returns Error when client is null', () async {
      expect(await HistoryApi(() => null).projects(),
          isA<Error<List<HistoryProject>>>());
    });

    test('sessions() returns Error when client is null', () async {
      expect(
        await HistoryApi(() => null)
            .sessions(projectDir: '/p', limit: 100, offset: 0),
        isA<Error<HistorySessionPage>>(),
      );
    });

    test('transcript() returns Error when client is null', () async {
      expect(await HistoryApi(() => null).transcript(transcriptPath: '/t.jsonl'),
          isA<Error<List<Chunk>>>());
    });
  });
}
