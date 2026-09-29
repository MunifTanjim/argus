import 'package:argus/models/project.dart';
import 'package:argus/state/setup_text.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('cleanOutput strips ANSI, applies \\r overwrites, drops controls', () {
    expect(cleanOutput('\x1b[32mok\x1b[0m'), 'ok');
    expect(cleanOutput('10%\r50%\r100%'), '100%');
    expect(cleanOutput('line\r\nnext'), 'line\nnext');
    expect(cleanOutput('a\x07b\tc'), 'ab\tc');
    expect(cleanOutput('\x1b]0;title\x07text'), 'text');
  });

  test('commandLine keeps the first line and marks the rest', () {
    expect(commandLine('  make deps  '), 'make deps');
    expect(commandLine('make\tdeps\nnpm ci'), 'make deps …');
    expect(commandLine('make\n   \n'), 'make');
    expect(commandLine(''), '');
  });

  test('setupHeadline follows the TUI setup block', () {
    expect(setupHeadline(null), isNull);
    expect(setupHeadline(const SetupRun(state: 'ok', command: 'make')), isNull);
    expect(
      setupHeadline(
        const SetupRun(state: 'failed', command: 'make', exitCode: 2),
      ),
      'Setup failed (exit 2) · make',
    );
    expect(
      setupHeadline(const SetupRun(state: 'failed', exitCode: 2)),
      'Setup failed (exit 2)',
    );
    expect(setupHeadline(const SetupRun(state: 'failed')), 'Setup failed');
    expect(
      setupHeadline(const SetupRun(state: 'failed', command: 'make')),
      'Setup failed · make',
    );
    expect(setupHeadline(const SetupRun(state: 'running')), 'Setup running');
    expect(
      setupHeadline(const SetupRun(state: 'running', command: 'make\nx')),
      'Setup running · make …',
    );
  });

  test('setupTail keeps the last 8 non-empty cleaned lines', () {
    final out = [
      for (var i = 1; i <= 12; i++) 'l$i',
      '',
      '\x1b[1mlast\x1b[0m',
    ].join('\n');
    expect(setupTail(SetupRun(state: 'failed', outputTail: out)), [
      'l6',
      'l7',
      'l8',
      'l9',
      'l10',
      'l11',
      'l12',
      'last',
    ]);
    expect(setupTail(const SetupRun(state: 'running')), isEmpty);
  });
}
