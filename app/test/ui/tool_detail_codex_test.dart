import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/ui/tool_detail.dart';
import 'package:argus/ui/tool_detail_codex.dart';

Widget _wrap(Item i) => MaterialApp(
    home: Scaffold(body: SingleChildScrollView(child: toolDetailBody(i))));

void main() {
  group('result helpers', () {
    test('splitExecResult splits at the Output marker', () {
      const r = 'Chunk ID: x\nProcess exited with code 0\nOutput:\nhello\nworld';
      final s = splitExecResult(r);
      expect(s.hasMarker, isTrue);
      expect(s.head, 'Chunk ID: x\nProcess exited with code 0\nOutput:');
      expect(s.output, 'hello\nworld');
    });

    test('splitExecResult with no marker keeps whole head', () {
      final s = splitExecResult('backgrounded');
      expect(s.hasMarker, isFalse);
      expect(s.output, '');
    });

    test('agentStatus reads the single state/message pair', () {
      final s = agentStatus({'completed': 'all done'});
      expect(s?.state, 'completed');
      expect(s?.message, 'all done');
      expect(agentStatus('nope'), isNull);
      expect(agentStatus(const {}), isNull);
    });

    test('agentName resolves via subagents, falls back to id', () {
      const it = Item(
        id: 'i',
        kind: ItemKind.subagent,
        subagents: [Subagent(id: 'a1', name: 'Volta')],
      );
      expect(agentName(it, 'a1'), 'Volta');
      expect(agentName(it, 'a2'), 'a2');
    });
  });

  group('renderers', () {
    testWidgets('exec_command shows workdir, command and split output',
        (tester) async {
      await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'exec_command',
        toolInput:
            '{"cmd":"pwd","workdir":"/repo","yield_time_ms":1000,"max_output_tokens":2000}',
        result: 'Chunk ID: x\nProcess exited with code 0\nOutput:\n/repo/out',
      )));
      expect(find.textContaining('pwd'), findsOneWidget);
      expect(find.textContaining('/repo/out'), findsWidgets);
      expect(find.textContaining('yield 1000ms'), findsOneWidget);
    });

    testWidgets('update_plan shows steps with status glyphs', (tester) async {
      await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'update_plan',
        toolInput:
            '{"plan":[{"step":"Alpha","status":"completed"},{"step":"Beta","status":"in_progress"}]}',
      )));
      expect(find.textContaining('☑ Alpha'), findsOneWidget);
      expect(find.textContaining('◐ Beta'), findsOneWidget);
    });

    testWidgets('web_search shows the query', (tester) async {
      await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.tool,
        toolName: 'web_search',
        toolInput:
            '{"type":"search","query":"example domain","queries":["example domain"]}',
      )));
      expect(find.textContaining('example domain'), findsOneWidget);
    });

    testWidgets('wait_agent shows targets by name and their status',
        (tester) async {
      await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.subagent,
        toolName: 'wait_agent',
        toolInput: '{"targets":["a1"],"timeout_ms":30000}',
        result: '{"status":{"a1":{"completed":"all done"}}}',
        subagents: [Subagent(id: 'a1', name: 'Volta')],
      )));
      expect(find.textContaining('Waiting on Volta', findRichText: true),
          findsWidgets);
      expect(find.textContaining('completed', findRichText: true), findsWidgets);
      expect(find.textContaining('all done', findRichText: true), findsWidgets);
    });

    testWidgets('close_agent shows the closed agent and previous status',
        (tester) async {
      await tester.pumpWidget(_wrap(const Item(
        id: 'i',
        kind: ItemKind.subagent,
        toolName: 'close_agent',
        toolInput: '{"target":"a1"}',
        result: '{"previous_status":{"completed":"bye"}}',
        subagents: [Subagent(id: 'a1', name: 'Volta')],
      )));
      expect(find.textContaining('Closed Volta', findRichText: true),
          findsWidgets);
      expect(find.textContaining('bye', findRichText: true), findsWidgets);
    });
  });
}
