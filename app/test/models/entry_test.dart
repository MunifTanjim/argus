import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/entry.dart';

const _aiEntries = '''
[{"id":"i0","kind":"thinking","text":"hmm","signature":true},
 {"id":"i1","kind":"tool","toolName":"Bash","toolId":"t1","inputPreview":"ls -la","result":"ok"},
 {"id":"i2","kind":"text","text":"done"},
 {"id":"1.end","kind":"turn_end","timestamp":"2026-06-21T00:00:00Z","modelName":"Opus 4.8","modelColor":"#d3869b",
  "thinking":1,"toolCount":1,
  "usage":{"input":100,"output":20,"cacheRead":30,"cacheCreation":5},
  "stopReason":"end_turn","durationMs":1234,
  "hasContext":true,"contextPct":42.5,"contextFirstPct":40,"contextDeltaTokens":12}]''';

const _userChunk = '{"id":"u1","kind":"user","text":"hello there"}';
const _sysChunk =
    '{"id":"s1","kind":"system","summary":"compacted","detail":"freed 10k","isError":false}';

void main() {
  test('AI turn entries parse items, usage and context', () {
    final es = (jsonDecode(_aiEntries) as List)
        .map((e) => Entry.fromJson(e as Map<String, dynamic>))
        .toList();
    expect(es.length, 4);
    expect(es[0].kind, EntryKind.thinking);
    expect(es[0].signature, isTrue);
    expect(es[1].kind, EntryKind.tool);
    expect(es[1].toolName, 'Bash');
    expect(es[1].inputPreview, 'ls -la');
    expect(es[2].kind, EntryKind.text);
    final end = es[3];
    expect(end.kind, EntryKind.turnEnd);
    expect(end.modelName, 'Opus 4.8');
    expect(end.modelColor, '#d3869b');
    expect(end.usage.context, 135); // 100 + 30 + 5
    expect(end.usage.output, 20);
    expect(end.durationMs, 1234);
    expect(end.contextPct, 42.5);
  });

  test('parses a turn_end entry', () {
    final e = Entry.fromJson({
      'id': '1.end',
      'kind': 'turn_end',
      'modelName': 'Opus 4.8',
      'usage': {'output': 30},
      'durationMs': 1200,
      'interrupted': true,
    });
    expect(e.kind, EntryKind.turnEnd);
    expect(e.usage.output, 30);
    expect(e.interrupted, isTrue);
  });

  test('user and system entries parse', () {
    final u = Entry.fromJson(jsonDecode(_userChunk) as Map<String, dynamic>);
    expect(u.kind, EntryKind.user);
    expect(u.text, 'hello there');

    final s = Entry.fromJson(jsonDecode(_sysChunk) as Map<String, dynamic>);
    expect(s.kind, EntryKind.system);
    expect(s.summary, 'compacted');
    expect(s.detail, 'freed 10k');
  });

  test('TranscriptDelta parses envelope', () {
    final d = TranscriptDelta.fromJson(jsonDecode(
            '{"sub_id":"ab","from_index":2,"entries":[$_userChunk]}')
        as Map<String, dynamic>);
    expect(d.subId, 'ab');
    expect(d.fromIndex, 2);
    expect(d.entries.single.id, 'u1');
  });

  test('null/missing fields degrade gracefully', () {
    final c = Entry.fromJson(
        jsonDecode('{"id":"x","kind":"weird"}') as Map<String, dynamic>);
    expect(c.kind, EntryKind.unknown);
    expect(c.subagents, isEmpty);
    expect(c.usage.context, 0);
  });
}
