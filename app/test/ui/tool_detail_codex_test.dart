import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/entry.dart';
import 'package:argus/ui/tool_detail.dart';
import 'package:argus/ui/tool_detail_codex.dart';
import 'package:argus/ui/tool_registry.dart';

Widget _wrap(Entry i) => MaterialApp(
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
      // States with no message serialize bare.
      expect(agentStatus('running'), (state: 'running', message: ''));
      expect(agentStatus(''), isNull);
      expect(agentStatus(42), isNull);
      expect(agentStatus(const {}), isNull);
    });

    test('agentName resolves via subagents, falls back to id', () {
      const it = Entry(
        id: 'i',
        kind: EntryKind.subagent,
        subagents: [Subagent(id: 'a1', name: 'Volta')],
      );
      expect(agentName(it, 'a1'), 'Volta');
      expect(agentName(it, 'a2'), 'a2');
    });
  });

  group('renderers', () {
    testWidgets('exec_command shows workdir, command and split output',
        (tester) async {
      await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
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
      await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'update_plan',
        toolInput:
            '{"plan":[{"step":"Alpha","status":"completed"},{"step":"Beta","status":"in_progress"}]}',
      )));
      expect(find.textContaining('☑ Alpha'), findsOneWidget);
      expect(find.textContaining('◐ Beta'), findsOneWidget);
    });

    testWidgets('web_search shows the query', (tester) async {
      await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'web_search',
        toolInput:
            '{"type":"search","query":"example domain","queries":["example domain"]}',
      )));
      expect(find.textContaining('example domain'), findsOneWidget);
    });

    testWidgets('wait_agent shows targets by name and their status',
        (tester) async {
      await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.subagent,
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
      await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.subagent,
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

  group('codex parsers', () {
    test('parseCodexPatch reads files, moves and lines', () {
      final files = parseCodexPatch(
          '*** Begin Patch\n*** Add File: /r/n\n+hi\n*** Update File: /r/a\n*** Move to: /r/b\n@@ ctx\n-old\n+new\n*** Delete File: /r/d\n*** End Patch\n')!;
      expect(files.map((f) => '${f.op}:${f.path}:${f.moveTo}').toList(),
          ['add:/r/n:', 'update:/r/a:/r/b', 'delete:/r/d:']);
      expect(files[1].lines.map((l) => '${l.kind}${l.text}').toList(),
          ['@ctx', '-old', '+new']);
    });
    test('parseCodexPatch rejects non-patches', () {
      expect(parseCodexPatch('echo hi'), isNull);
      expect(parseCodexPatch('*** Begin Patch\n+orphan\n*** End Patch'), isNull);
    });
    test('mcpDisplayName', () {
      expect(mcpDisplayName('mcp__github__create_issue'),
          'github › create_issue');
      expect(mcpDisplayName('mcp__server'), isNull);
      expect(mcpDisplayName('exec'), isNull);
    });
    test('splitCodexAnswer separates the note', () {
      expect(splitCodexAnswer(['Large', 'user_note: xl']),
          (label: 'Large', note: 'xl'));
    });
    test('toolMeta resolves MCP names', () {
      expect(toolMeta('codex', 'mcp__github__create_issue')?.display,
          'github › create_issue');
      expect(toolMeta('codex', 'mcp__server'), isNull);
    });
  });

  group('codex renderers', () {
    testWidgets('apply_patch shows per-file diff', (tester) async {
      await tester.pumpWidget(_wrap(const Entry(
          id: 'i',
          kind: EntryKind.tool,
          toolName: 'apply_patch',
          toolInput:
              '*** Begin Patch\n*** Update File: /r/a.go\n*** Move to: /r/b.go\n@@\n-old\n+new\n*** End Patch\n',
          result: 'Exit code: 0\nWall time: 0.1 seconds\nOutput:\nSuccess.\n')));
      expect(find.text('M /r/a.go → /r/b.go'), findsOneWidget);
      expect(find.text('@@\n-old\n+new', findRichText: true), findsOneWidget);
      expect(find.textContaining('Success.'), findsNothing);
    });

    testWidgets('exec shows script and output', (tester) async {
      await tester.pumpWidget(_wrap(const Entry(
          id: 'i',
          kind: EntryKind.tool,
          toolName: 'exec',
          toolInput: 'text(await tools.exec_command({cmd:"ls"}));',
          result: 'Script completed\nWall time 0.2 seconds\nOutput:\nREADME.md')));
      expect(find.textContaining('tools.exec_command', findRichText: true),
          findsWidgets);
      expect(find.textContaining('README.md', findRichText: true),
          findsWidgets);
    });

    testWidgets('request_user_input marks answer and note', (tester) async {
      await tester.pumpWidget(_wrap(const Entry(
          id: 'i',
          kind: EntryKind.tool,
          toolName: 'request_user_input',
          toolInput:
              '{"questions":[{"id":"c","header":"Color","question":"Pick a color","options":[{"label":"Red","description":"r"}]}]}',
          result:
              '{"answers":{"c":{"answers":["None of the above","user_note: green actually"]}}}')));
      expect(find.text('○ Red'), findsOneWidget);
      expect(find.text('Answer: None of the above'), findsOneWidget);
      expect(find.text('Note: green actually'), findsOneWidget);
    });

    testWidgets('request_user_input_async lists questions', (tester) async {
      await tester.pumpWidget(_wrap(const Entry(
          id: 'i',
          kind: EntryKind.tool,
          toolName: 'request_user_input_async',
          toolInput: '{"questions":[{"title":"Pick a color","options":["Red"]}]}',
          result: '{"accepted":true}')));
      expect(find.text('Pick a color'), findsOneWidget);
      expect(find.text('• Red'), findsOneWidget);
      expect(find.text('Asked without waiting; answered in a later message.'),
          findsOneWidget);
    });

    testWidgets('view_image shows path and attachment', (tester) async {
      await tester.pumpWidget(_wrap(const Entry(
          id: 'i',
          kind: EntryKind.tool,
          toolName: 'view_image',
          toolInput: '{"path":"/tmp/x.png","detail":"high"}',
          result: '[image, detail: high]')));
      expect(find.textContaining('/tmp/x.png', findRichText: true),
          findsWidgets);
      expect(find.text('image attached'), findsOneWidget);
    });

    testWidgets('mcp shows arguments as key/value', (tester) async {
      await tester.pumpWidget(_wrap(const Entry(
          id: 'i',
          kind: EntryKind.tool,
          toolName: 'mcp__github__create_issue',
          toolInput: '{"title":"Bug","repo":"o/r"}',
          result: '{"number":7}')));
      expect(find.textContaining('title:', findRichText: true), findsWidgets);
      expect(find.textContaining('Bug', findRichText: true), findsWidgets);
    });
  
    for (final c in [
      (
        'collaboration.wait_agent',
        '{"timeout_ms":30000}',
        '{"message":"Wait completed.","timed_out":false}',
        ['Waiting on agents', 'timeout 30s', 'Wait completed.']
      ),
      (
        'collaboration.send_message',
        '{"target":"/root/a","message":"check the tests"}',
        '',
        ['To ', '/root/a', 'check the tests']
      ),
      (
        'collaboration.followup_task',
        '{"target":"/root/a","message":"gAAAAABq"}',
        '',
        ['/root/a', 'message encrypted']
      ),
      (
        'collaboration.interrupt_agent',
        '{"target":"/root/a"}',
        '{"previous_status":"running"}',
        ['Interrupted ', '/root/a', 'Previous status', 'running']
      ),
      (
        'collaboration.list_agents',
        '{}',
        '{"agents":[{"agent_name":"/root/a","agent_status":{"completed":"done"}},{"agent_name":"/root/b","agent_status":"running"}]}',
        ['/root/a', 'completed', 'done', '/root/b', 'running']
      ),
      (
        'clock.sleep',
        '{"duration_ms":10000}',
        'Wall time: 10.0047 seconds\nSleep completed.',
        ['Sleep ', '10s', 'Sleep completed.']
      ),
      (
        'wait',
        '{"cell_id":"1","yield_time_ms":25000}',
        'Script completed\nWall time 18.1 seconds\nOutput:\nending...',
        ['Waiting on cell ', 'yield 25s', 'ending...']
      ),
    ]) {
      testWidgets('${c.$1} renders', (tester) async {
        await tester.pumpWidget(_wrap(Entry(
            id: 'i',
            kind: EntryKind.tool,
            toolName: c.$1,
            toolInput: c.$2,
            result: c.$3.isEmpty ? null : c.$3)));
        for (final want in c.$4) {
          expect(find.textContaining(want, findRichText: true), findsWidgets,
              reason: '${c.$1}: $want');
        }
        expect(find.textContaining('gAAAAA', findRichText: true), findsNothing);
      });
    }

    test('msDuration', () {
      expect(msDuration(10000), '10s');
      expect(msDuration(1500), '1.5s');
      expect(msDuration(90000), '1m30s');
    });

    test('namespaced codex tools are registered; v1 keeps its views', () {
      for (final n in [
        'spawn_agent',
        'collaboration.send_message',
        'collaboration.list_agents',
        'clock.sleep',
        'wait'
      ]) {
        expect(toolMeta('codex', n), isNotNull, reason: n);
      }
      expect(isAgentRefTool('close_agent'), isTrue);
      expect(isAgentRefTool('collaboration.wait_agent'), isFalse);
    });

    for (final c in [
      (
        'request_user_input',
        '{"questions":[{"header":"Text","id":"t","question":"What exact message?"}]}',
        'request_user_input requires non-empty options for every question'
      ),
      (
        'request_user_input_async',
        '{"questions":[{"title":"Pick","options":["A"]}]}',
        'failed to parse function arguments: unknown field `question`'
      ),
    ]) {
      testWidgets('${c.$1} rejected by codex shows the error', (tester) async {
        await tester.pumpWidget(_wrap(Entry(
            id: 'i',
            kind: EntryKind.tool,
            toolName: c.$1,
            toolInput: c.$2,
            result: c.$3,
            resultIsError: true)));
        expect(find.text('Error'), findsOneWidget);
        expect(find.textContaining(c.$3, findRichText: true), findsWidgets);
        expect(find.textContaining('Asked without waiting'), findsNothing);
      });
    }
  });
}
