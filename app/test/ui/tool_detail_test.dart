import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/entry.dart';
import 'package:argus/ui/tool_detail.dart';
import 'package:argus/ui/tool_registry.dart';

Widget _wrap(Entry i) => MaterialApp(
    home: Scaffold(body: SingleChildScrollView(child: toolDetailBody(i))));

void main() {
  testWidgets('Bash shows command and result', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'Bash',
        toolInput: '{"command":"ls -la","description":"list"}',
        result: 'total 0')));
    expect(find.textContaining('ls -la'), findsOneWidget);
    expect(find.textContaining('total 0'), findsOneWidget);
  });

  testWidgets('Bash command renders as a copyable code block', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'Bash',
        toolInput: '{"command":"echo one\\necho two"}')));
    expect(find.text('bash'), findsOneWidget); // code block header label
    await tester.tap(find.byIcon(Icons.copy));
    await tester.pump();
    expect(find.text('Copied'), findsOneWidget);
  });

  testWidgets('EnterPlanMode result is labelled markdown', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'EnterPlanMode',
        toolInput: '{}',
        result: '## Plan\n\n- step one')));
    expect(find.text('markdown'), findsOneWidget); // code block header label
  });

  testWidgets('ExitPlanMode shows plan file, plan and result as markdown',
      (tester) async {
    await tester.pumpWidget(_wrap(Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'ExitPlanMode',
        toolInput: jsonEncode({
          'plan': '## Heading\n\n- step one',
          'planFilePath': '/home/u/.claude/plans/x.md',
          'allowedPrompts': [
            {'tool': 'Bash', 'prompt': 'y'}
          ],
        }),
        result: '## Approved\n\nGo ahead now')));
    // Plan file path rendered pretty, not as raw JSON.
    expect(find.text('Plan file'), findsOneWidget);
    expect(find.textContaining('/home/u/.claude/plans/x.md'), findsOneWidget);
    // Plan and result render as markdown, not highlighted code blocks.
    expect(find.text('markdown'), findsNothing);
    expect(find.text('json'), findsNothing);
    expect(find.textContaining('step one'), findsOneWidget);
    expect(find.textContaining('Go ahead now'), findsOneWidget);
  });

  testWidgets('ExitPlanMode collapses a long plan behind a toggle',
      (tester) async {
    final longPlan = List.generate(30, (i) => '- item $i').join('\n');
    await tester.pumpWidget(_wrap(Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'ExitPlanMode',
        toolInput: jsonEncode({'plan': longPlan, 'planFilePath': '/p.md'}))));

    expect(find.text('Show more'), findsOneWidget);
    expect(find.text('Show less'), findsNothing);
    await tester.tap(find.byKey(const Key('plan-toggle')));
    await tester.pump();
    expect(find.text('Show less'), findsOneWidget);
  });

  for (final tool in const ['WebFetch', 'WebSearch']) {
    testWidgets('$tool result renders as markdown', (tester) async {
      await tester.pumpWidget(_wrap(Entry(
          id: 'i',
          kind: EntryKind.tool,
          toolName: tool,
          toolInput:
              tool == 'WebFetch' ? '{"url":"https://x.dev"}' : '{"query":"q"}',
          result: '# Heading\n\nbody text')));
      expect(find.text('markdown'), findsNothing); // no code-block header
      expect(find.textContaining('Heading'), findsOneWidget);
      expect(find.textContaining('body text'), findsOneWidget);
      // Input and output are clearly labelled.
      expect(find.text(tool == 'WebFetch' ? 'URL' : 'Query'), findsOneWidget);
      expect(find.text('Result'), findsOneWidget);
    });
  }

  testWidgets('Read infers language from the file extension', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'Read',
        toolInput: '{"file_path":"/x/foo.py"}',
        result: '     1\tprint("hi")')));
    expect(find.text('python'), findsOneWidget); // .py → python, not auto-detect
  });

  testWidgets('Read result hides the line-number toggle', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'Read',
        toolInput: '{"file_path":"/a.dart"}',
        result: '     1\tline one\n     2\tline two')));
    expect(find.byIcon(Icons.format_list_numbered), findsNothing);
  });

  testWidgets('Grep shows pattern header', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'Grep',
        toolInput: '{"pattern":"foo","path":"lib"}',
        result: 'lib/a.dart:1')));
    expect(find.textContaining('foo'), findsOneWidget);
    expect(find.textContaining('lib'), findsWidgets);
  });

  testWidgets('TodoWrite renders checklist', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'TodoWrite',
        toolInput:
            '{"todos":[{"content":"do a","status":"completed","activeForm":"doing a"},{"content":"do b","status":"in_progress","activeForm":"doing b"}]}')));
    expect(find.textContaining('do a'), findsOneWidget);
    expect(find.textContaining('doing b'), findsOneWidget);
  });

  testWidgets('generic fallback shows input and result', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'MysteryTool',
        toolInput: '{"x":1}',
        result: 'done')));
    expect(find.textContaining('done'), findsOneWidget);
  });

  // opencode tool calls: lowercase names and their own input key shapes.
  testWidgets('opencode read uses the path key and infers language',
      (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'read',
        toolInput: '{"path":"/x/foo.py"}',
        result: '10: print("hi")')));
    expect(find.text('python'), findsOneWidget);
    expect(find.textContaining('/x/foo.py'), findsOneWidget);
  });

  testWidgets('opencode edit renders a diff from oldString/newString',
      (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'edit',
        toolInput:
            '{"path":"/x/a.dart","oldString":"var x = 1;","newString":"var x = 2;"}',
        result: 'Edited /x/a.dart (1 replacement)')));
    expect(find.text('diff'), findsOneWidget); // diff box header
    expect(find.textContaining('/x/a.dart'), findsWidgets);
  });

  testWidgets('opencode write renders new content as an all-add diff',
      (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'write',
        toolInput: '{"filePath":"/x/new.txt","content":"hello\\nworld"}')));
    expect(find.text('diff'), findsOneWidget);
  });

  testWidgets('opencode bash shows the command', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'bash',
        toolInput: '{"command":"go build ./...","description":"build"}',
        result: 'ok')));
    expect(find.textContaining('go build'), findsOneWidget);
  });

  testWidgets('opencode execute renders code, not raw JSON', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'execute',
        toolInput: '{"code":"print(2 + 2)"}',
        result: '4')));
    expect(find.textContaining('print(2 + 2)'), findsOneWidget);
    expect(find.textContaining('"code"'), findsNothing);
  });

  testWidgets('opencode grep uses the include key for scope', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'grep',
        toolInput: '{"pattern":"foo","path":"internal","include":"*.go"}',
        result: 'internal/a.go:1')));
    expect(find.textContaining('foo'), findsOneWidget);
    expect(find.textContaining('*.go'), findsOneWidget);
  });

  testWidgets('opencode skill uses the id key', (tester) async {
    await tester.pumpWidget(_wrap(const Entry(
        id: 'i',
        kind: EntryKind.tool,
        toolName: 'skill',
        toolInput: '{"id":"my-skill"}',
        result: '<skill_content>body</skill_content>')));
    expect(find.textContaining('my-skill'), findsOneWidget);
  });

  test('opencode tool names are registered with display and category', () {
    expect(toolMeta('opencode', 'read')!.display, 'Read');
    expect(toolMeta('opencode', 'read')!.category, ToolCategory.read);
    expect(toolMeta('opencode', 'edit')!.category, ToolCategory.edit);
    expect(toolMeta('opencode', 'bash')!.category, ToolCategory.bash);
    expect(toolMeta('opencode', 'todowrite')!.display, 'Todo');
    expect(toolMeta('opencode', 'question')!.category, ToolCategory.other);
  });

  test('answeredAnswers parses question→answer pairs', () {
    const r =
        'Your questions have been answered: "Pick one"="A", "Colors"="red, blue"';
    final a = answeredAnswers(r, ['Pick one', 'Colors', 'Absent']);
    expect(a['Pick one'], 'A');
    expect(a['Colors'], 'red, blue');
    expect(a.containsKey('Absent'), isFalse);
  });

  test('answeredAnswers handles quotes inside questions and answers', () {
    const r = 'Your questions have been answered: '
        '"What should "chat" do?"="Interrupt (Recommended)", '
        '"Esc back to "back", how?"="Use `c` ("chat" slot)". '
        'You can now continue with these answers in mind.';
    final a =
        answeredAnswers(r, ['Esc back to "back", how?', 'What should "chat" do?']);
    expect(a['What should "chat" do?'], 'Interrupt (Recommended)');
    expect(a['Esc back to "back", how?'], 'Use `c` ("chat" slot)');
  });

  test('toolMeta prefers the agent\'s own entry', () {
    // send_message is antigravity's; codex v2 records its own as
    // collaboration.send_message.
    expect(toolMeta('antigravity', 'send_message')?.display, 'Send Message');
    expect(toolMeta('codex', 'send_message'), isNull);
    // A known agent does not borrow another agent's tool.
    expect(toolMeta('opencode', 'run_command'), isNull);
    // An unknown agent falls back to any agent's entry.
    expect(toolMeta(null, 'run_command')?.display, 'Run Command');
    // MCP tools resolve for every agent.
    expect(toolMeta('claude', 'mcp__github__create_issue'), isNotNull);
    // Codex records skill loads under Claude's Skill name.
    expect(toolMeta('codex', 'Skill'), isNotNull);
  });
}
