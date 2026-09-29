import '../models/project.dart';

// CSI, OSC (BEL or ST terminated), and two-byte escape sequences.
final _ansi = RegExp(
  r'\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])',
);

bool _isControl(int r) => r < 0x20 || (r >= 0x7f && r < 0xa0);

String _dropControls(String s) =>
    String.fromCharCodes(s.runes.where((r) => r == 0x09 || !_isControl(r)));

/// A port of Go wsscript.CleanOutput. The text after a line's last \r is a
/// progress bar's final state.
String cleanOutput(String s) => s
    .split('\n')
    .map((l) {
      if (l.endsWith('\r')) l = l.substring(0, l.length - 1);
      final j = l.lastIndexOf('\r');
      if (j >= 0) l = l.substring(j + 1);
      return _dropControls(l.replaceAll(_ansi, ''));
    })
    .join('\n');

/// A port of the TUI commandLine.
String commandLine(String cmd) {
  final trimmed = cmd.trim();
  final nl = trimmed.indexOf('\n');
  final first = nl < 0 ? trimmed : trimmed.substring(0, nl);
  final rest = nl < 0 ? '' : trimmed.substring(nl + 1);
  final line = _dropControls(
    first.replaceAll('\t', ' ').replaceAll(_ansi, ''),
  ).trim();
  return rest.trim().isEmpty ? line : '$line …';
}

String? setupHeadline(SetupRun? run) {
  if (run == null || run.state == 'ok') return null;
  final cmd = commandLine(run.command);
  final withCmd = cmd.isEmpty ? '' : ' · $cmd';
  if (run.state == 'failed') {
    final exit = run.exitCode > 0 ? ' (exit ${run.exitCode})' : '';
    return 'Setup failed$exit$withCmd';
  }
  return 'Setup running$withCmd';
}

List<String> setupTail(SetupRun run) {
  final lines = [
    for (final l in cleanOutput(run.outputTail).split('\n'))
      if (l.trim().isNotEmpty) l,
  ];
  return lines.length > 8 ? lines.sublist(lines.length - 8) : lines;
}
