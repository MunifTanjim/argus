import 'package:argus/models/entry.dart';
import 'package:argus/ui/run_summary.dart';
import 'package:flutter_test/flutter_test.dart';

var _n = 0;

Entry _tool(String name, {String? preview, bool failed = false}) => Entry(
      id: 'e${_n++}',
      kind: EntryKind.tool,
      toolName: name,
      inputPreview: preview,
      resultIsError: failed,
    );

const _thinking = Entry(id: 'th', kind: EntryKind.thinking, text: 'hmm');

String _text(List<Entry> run, {String agent = 'claude'}) =>
    summarizeRun(run, agent: agent).label;

void main() {
  test('read: one names the file, many count them', () {
    expect(_text([_tool('Read', preview: '/src/auth.dart')]), 'read auth.dart');
    expect(
        _text([_tool('Read', preview: 'a'), _tool('Read', preview: 'b')]),
        'read 2 files');
  });

  test('grep and glob merge into searched', () {
    expect(_text([_tool('Grep', preview: 'token')]), 'searched');
    expect(_text([_tool('Grep'), _tool('Glob'), _tool('Grep')]), 'searched 3×');
  });

  test('edit and write merge into edited', () {
    expect(_text([_tool('Edit', preview: 'lib/a.dart')]), 'edited a.dart');
    expect(_text([_tool('Edit', preview: 'a'), _tool('Write', preview: 'b')]),
        'edited 2 files');
  });

  test('bash: one names the command, many count them', () {
    expect(_text([_tool('Bash', preview: 'flutter test')]), 'ran flutter test');
    expect(_text([_tool('Bash'), _tool('Bash')]), 'ran 2 commands');
  });

  test('another agent maps through its own registry', () {
    expect(_text([_tool('exec_command', preview: 'ls')], agent: 'codex'),
        'ran ls');
  });

  test('web, todo, skill, and subagent ops', () {
    expect(_text([_tool('WebFetch')]), 'used the web');
    expect(_text([_tool('WebFetch'), _tool('WebSearch')]), 'used the web 2×');
    expect(_text([_tool('TodoWrite'), _tool('TodoWrite')]), 'updated tasks');
    expect(
        _text([
          const Entry(
              id: 's', kind: EntryKind.skill, toolName: 'Skill', inputPreview: 'pdf'),
        ]),
        'used skill pdf');
    expect(
        _text([
          const Entry(id: 'w', kind: EntryKind.subagent, toolName: 'wait_agent'),
        ], agent: 'codex'),
        'managed subagents');
  });

  test('unregistered tools read by name, or as other tools', () {
    expect(_text([_tool('mcp__x__do_thing')]), 'used x › do_thing');
    expect(_text([_tool('ZZSolo')]), 'used ZZSolo');
    expect(_text([_tool('ZZAlpha'), _tool('ZZBeta')]), 'used 2 other tools');
  });

  test('thinking leads with thought; thinking alone is thought', () {
    expect(_text([_thinking]), 'thought');
    expect(_text([_thinking, _tool('Read', preview: 'a.dart'), _thinking]),
        'thought, read a.dart');
  });

  test('phrases keep the order the tools first ran', () {
    expect(
        _text([
          _tool('Bash', preview: 'make'),
          _tool('Read', preview: 'x'),
          _tool('Bash', preview: 'make test'),
        ]),
        'ran 2 commands, read x');
  });

  test('more than 4 phrases end in +N more', () {
    expect(
        _text([
          _tool('Read', preview: 'a'),
          _tool('Grep'),
          _tool('Edit', preview: 'b'),
          _tool('Bash', preview: 'c'),
          _tool('WebFetch'),
          _tool('TodoWrite'),
        ]),
        'read a, searched, edited b, ran c, +2 more');
  });

  test('failed tools are counted, not named', () {
    final s = summarizeRun([
      _tool('Bash', preview: 'flutter test', failed: true),
      _tool('Read', preview: 'a'),
      _tool('Bash', preview: 'make', failed: true),
    ], agent: 'claude');
    expect(s.failed, 2);
    expect(s.label, 'ran 2 commands, read a, 2 failed');
  });

  test('a long command is cut to 30 characters', () {
    expect(
        _text([_tool('Bash', preview: 'flutter test --tags golden --update-goldens')]),
        'ran flutter test --tags golden --u…');
  });

  test('no preview drops the target', () {
    expect(_text([_tool('Read')]), 'read a file');
    expect(_text([_tool('Edit')]), 'edited a file');
    expect(_text([_tool('Bash')]), 'ran a command');
    expect(
        _text([const Entry(id: 's', kind: EntryKind.skill, toolName: 'Skill')]),
        'used a skill');
  });

  group('agent preview formats', () {
    test('claude bash previews put the description first', () {
      expect(
          _text([_tool('Bash', preview: 'Run the Flutter tests: flutter test')]),
          'ran flutter test');
    });

    test('claude read and edit previews drop the line count', () {
      expect(_text([_tool('Read', preview: 'lib/auth.dart - 120 lines')]),
          'read auth.dart');
      expect(_text([_tool('Edit', preview: 'ui/auth.dart - 3 -> 5 lines')]),
          'edited auth.dart');
    });

    test('antigravity prose previews name no target', () {
      expect(
          _text([_tool('view_file', preview: 'View a.go')], agent: 'antigravity'),
          'read a file');
      expect(
          _text([_tool('run_command', preview: 'Run tests')],
              agent: 'antigravity'),
          'ran a command');
    });
  });

  test('files are counted once each, not once per call', () {
    expect(
        _text([
          _tool('Edit', preview: 'ui/auth.dart - 1 line'),
          _tool('Edit', preview: 'ui/auth.dart - 3 lines'),
          _tool('Edit', preview: 'ui/auth.dart - 2 lines'),
        ]),
        'edited auth.dart');
    expect(
        _text([
          _tool('Read', preview: 'a.dart'),
          _tool('Read', preview: 'b.dart'),
          _tool('Read', preview: 'a.dart'),
        ]),
        'read 2 files');
  });

  test('an empty tool name reads as a tool', () {
    expect(_text([_tool('')]), 'used a tool');
  });

  test('groups count calls in the order they first ran', () {
    final s = summarizeRun([
      _thinking,
      _tool('Read', preview: 'a'),
      _tool('Grep'),
      _tool('Glob'),
      _tool('Read', preview: 'a'),
      _tool('Bash', preview: 'make', failed: true),
      _thinking,
    ], agent: 'claude');
    expect(s.thinking, 2);
    expect(s.groups, [
      (RunGroup.read, 2),
      (RunGroup.search, 2),
      (RunGroup.bash, 1),
    ]);
    expect(s.failed, 1);
  });

  test('claude TaskOutput and TaskStop count as shell work', () {
    expect(summarizeRun([_tool('TaskOutput'), _tool('TaskStop')], agent: 'claude')
        .groups, [(RunGroup.bash, 2)]);
  });

  test('a cut never splits an emoji', () {
    final cmd = '${'a' * 29}😀tail';
    expect(_text([_tool('bash', preview: cmd)], agent: 'opencode'),
        'ran ${'a' * 29}😀…');
  });
}
