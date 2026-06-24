import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/chunk.dart';

const _withTrace = '''
{"id":"c1","kind":"ai","items":[
  {"id":"i0","kind":"subagent","subagentType":"Explore","subagentDesc":"find x",
   "agentId":"a1","hasTrace":true,
   "trace":[
     {"id":"t0","kind":"ai","items":[
       {"id":"ti0","kind":"tool","toolName":"Grep","inputPreview":"foo"}]}]}]}''';

void main() {
  test('subagent item parses a nested trace', () {
    final c = Chunk.fromJson(jsonDecode(_withTrace) as Map<String, dynamic>);
    final sub = c.items.single;
    expect(sub.kind, ItemKind.subagent);
    expect(sub.hasTrace, isTrue);
    expect(sub.agentId, 'a1');
    expect(sub.trace.length, 1);
    expect(sub.trace.single.kind, ChunkKind.ai);
    expect(sub.trace.single.items.single.toolName, 'Grep');
  });

  test('absent trace defaults to empty', () {
    final c = Chunk.fromJson(jsonDecode(
            '{"id":"c","kind":"ai","items":[{"id":"i","kind":"tool","toolName":"Bash"}]}')
        as Map<String, dynamic>);
    expect(c.items.single.trace, isEmpty);
  });
}
