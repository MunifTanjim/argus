import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/entry.dart';

const _withTrace = '''
{"id":"i0","kind":"subagent","subagents":[
  {"id":"a1","type":"Explore","desc":"find x","hasTrace":true,
   "trace":[
     {"id":"ti0","kind":"tool","toolName":"Grep","inputPreview":"foo"}]}]}''';

void main() {
  test('subagent entry parses a nested trace', () {
    final c = Entry.fromJson(jsonDecode(_withTrace) as Map<String, dynamic>);
    final sub = c.soleSubagent!;
    expect(c.kind, EntryKind.subagent);
    expect(sub.hasTrace, isTrue);
    expect(sub.id, 'a1');
    expect(sub.trace.length, 1);
    expect(sub.trace.single.kind, EntryKind.tool);
    expect(sub.trace.single.toolName, 'Grep');
  });

  test('absent subagents defaults to empty', () {
    final c = Entry.fromJson(jsonDecode(
            '{"id":"i","kind":"tool","toolName":"Bash"}')
        as Map<String, dynamic>);
    expect(c.subagents, isEmpty);
  });
}
