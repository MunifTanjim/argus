import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/chunk.dart';

const _aiChunk = '''
{"id":"c1","kind":"ai","timestamp":"2026-06-21T00:00:00Z","model":"claude-opus-4-8",
 "items":[
   {"id":"i0","kind":"thinking","text":"hmm","signature":true},
   {"id":"i1","kind":"tool","toolName":"Bash","toolId":"t1","inputPreview":"ls -la","result":"ok"},
   {"id":"i2","kind":"text","text":"done"}],
 "thinking":1,"toolCount":1,
 "usage":{"input":100,"output":20,"cacheRead":30,"cacheCreation":5},
 "stopReason":"end_turn","durationMs":1234,
 "hasContext":true,"contextPct":42.5,"contextFirstPct":40,"contextDeltaTokens":12}''';

const _userChunk = '{"id":"u1","kind":"user","text":"hello there"}';
const _sysChunk =
    '{"id":"s1","kind":"system","summary":"compacted","detail":"freed 10k","isError":false}';

void main() {
  test('AI chunk parses items, usage and context', () {
    final c = Chunk.fromJson(jsonDecode(_aiChunk) as Map<String, dynamic>);
    expect(c.kind, ChunkKind.ai);
    expect(c.model, 'claude-opus-4-8');
    expect(c.items.length, 3);
    expect(c.items[0].kind, ItemKind.thinking);
    expect(c.items[0].signature, isTrue);
    expect(c.items[1].kind, ItemKind.tool);
    expect(c.items[1].toolName, 'Bash');
    expect(c.items[1].inputPreview, 'ls -la');
    expect(c.usage.context, 135); // 100 + 30 + 5
    expect(c.usage.total, 155);
    expect(c.durationMs, 1234);
    expect(c.contextPct, 42.5);
  });

  test('user and system chunks parse', () {
    final u = Chunk.fromJson(jsonDecode(_userChunk) as Map<String, dynamic>);
    expect(u.kind, ChunkKind.user);
    expect(u.text, 'hello there');

    final s = Chunk.fromJson(jsonDecode(_sysChunk) as Map<String, dynamic>);
    expect(s.kind, ChunkKind.system);
    expect(s.summary, 'compacted');
    expect(s.detail, 'freed 10k');
  });

  test('TranscriptDelta parses envelope', () {
    final d = TranscriptDelta.fromJson(jsonDecode(
            '{"sub_id":"ab","from_index":2,"chunks":[$_userChunk]}')
        as Map<String, dynamic>);
    expect(d.subId, 'ab');
    expect(d.fromIndex, 2);
    expect(d.chunks.single.id, 'u1');
  });

  test('null/missing fields degrade gracefully', () {
    final c = Chunk.fromJson(
        jsonDecode('{"id":"x","kind":"weird"}') as Map<String, dynamic>);
    expect(c.kind, ChunkKind.unknown);
    expect(c.items, isEmpty);
    expect(c.usage.context, 0);
  });

  test('previewItem resolves the server-stamped id', () {
    final c = Chunk.fromJson({
      'id': 'c1',
      'kind': 'ai',
      'previewItemId': 'i3',
      'items': [
        {'id': 'i1', 'kind': 'text', 'text': 'first'},
        {'id': 'i3', 'kind': 'text', 'text': 'final answer'},
      ],
    });
    expect(c.previewItemId, 'i3');
    expect(c.previewItem?.text, 'final answer');
  });

  test('previewItem is null when no id stamped', () {
    final c = Chunk.fromJson({'id': 'c2', 'kind': 'user', 'text': 'hi'});
    expect(c.previewItem, isNull);
  });
}
