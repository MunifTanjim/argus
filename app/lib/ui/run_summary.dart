import 'package:flutter/widgets.dart';

import '../models/entry.dart';
import 'tool_registry.dart';

/// A folded run: icons with counts on screen, and words for a screen reader
/// ("thought, read 3 files, ran flutter test").
class RunSummary {
  const RunSummary(this.phrases, this.failed, this.thinking, this.groups);

  /// The words, without the failed count.
  final List<String> phrases;

  /// Tool calls in the run that failed.
  final int failed;

  /// The words and the failed count, for a screen reader. Commas, not dots:
  /// a screen reader can say "middle dot" aloud.
  String get label =>
      [...phrases, if (failed > 0) '$failed failed'].join(', ');

  /// Thinking entries in the run.
  final int thinking;

  /// Calls per group, in the order each group first ran.
  final List<(RunGroup, int)> groups;
}

/// The groups a run's entries count under. Kinds that read the same to a
/// person share one group (grep and glob both "search").
enum RunGroup { read, search, edit, bash, web, todo, skill, task, other }

/// A group's icon: the tool rows' category icon, so a folded run and its
/// open rows match.
IconData runGroupIcon(RunGroup g) => categoryIcon(switch (g) {
      RunGroup.read => ToolCategory.read,
      RunGroup.search => ToolCategory.grep,
      RunGroup.edit => ToolCategory.edit,
      RunGroup.bash => ToolCategory.bash,
      RunGroup.web => ToolCategory.web,
      RunGroup.todo => ToolCategory.todo,
      RunGroup.skill => ToolCategory.skill,
      RunGroup.task => ToolCategory.task,
      RunGroup.other => ToolCategory.other,
    });

const _maxPhrases = 4;
const _maxTarget = 30;

/// Summarizes [run] by tool category, so every agent reads the same once its
/// tools are in the registry; unregistered tools still count, by name.
RunSummary summarizeRun(List<Entry> run, {String? agent}) {
  var thinking = 0;
  var failed = 0;
  final groups = <RunGroup, List<Entry>>{};
  for (final e in run) {
    if (e.kind == EntryKind.thinking) {
      thinking++;
      continue;
    }
    if (e.resultIsError) failed++;
    (groups[_groupOf(e, agent)] ??= []).add(e);
  }
  final phrases = [
    for (final MapEntry(:key, :value) in groups.entries)
      _phrase(key, value, agent),
  ];
  final shown = phrases.take(_maxPhrases).toList();
  if (phrases.length > _maxPhrases) {
    shown.add('+${phrases.length - _maxPhrases} more');
  }
  return RunSummary(
    [if (thinking > 0) 'thought', ...shown],
    failed,
    thinking,
    [for (final MapEntry(:key, :value) in groups.entries) (key, value.length)],
  );
}

RunGroup _groupOf(Entry e, String? agent) {
  if (e.kind == EntryKind.skill) return RunGroup.skill;
  if (e.kind == EntryKind.subagent) return RunGroup.task;
  return switch (toolMeta(agent, e.toolName)?.category) {
    ToolCategory.read => RunGroup.read,
    ToolCategory.grep || ToolCategory.glob => RunGroup.search,
    ToolCategory.edit || ToolCategory.write => RunGroup.edit,
    ToolCategory.bash => RunGroup.bash,
    ToolCategory.web => RunGroup.web,
    ToolCategory.todo => RunGroup.todo,
    ToolCategory.skill => RunGroup.skill,
    ToolCategory.task => RunGroup.task,
    ToolCategory.other || null => RunGroup.other,
  };
}

String _phrase(RunGroup g, List<Entry> es, String? agent) {
  final n = es.length;
  final targets = [for (final e in es) _target(e, g, agent)];
  // A file read or edited more than once is still one file.
  final files = {
    for (final (i, t) in targets.indexed) t == null ? '#$i' : _base(t),
  };
  final file = files.length == 1 && targets.first != null ? files.single : null;
  final one = n == 1 ? targets.single : null;
  return switch (g) {
    RunGroup.read => file != null
        ? 'read $file'
        : (files.length == 1 ? 'read a file' : 'read ${files.length} files'),
    RunGroup.search => n == 1 ? 'searched' : 'searched $n×',
    RunGroup.edit => file != null
        ? 'edited $file'
        : (files.length == 1
            ? 'edited a file'
            : 'edited ${files.length} files'),
    RunGroup.bash => n > 1
        ? 'ran $n commands'
        : (one != null ? 'ran ${_cut(one)}' : 'ran a command'),
    RunGroup.web => n == 1 ? 'used the web' : 'used the web $n×',
    RunGroup.todo => 'updated tasks',
    RunGroup.skill => n > 1
        ? 'used $n skills'
        : (one != null ? 'used skill ${_cut(one)}' : 'used a skill'),
    RunGroup.task => 'managed subagents',
    RunGroup.other => n == 1
        ? 'used ${_name(es.single, agent)}'
        : 'used $n other tools',
  };
}

// Claude adds a line count to Read and Edit previews ("a.dart - 3 lines").
final _lineCount = RegExp(r'\s+-\s+\d+(\s+->\s+\d+)?\s+lines?$');

/// The file or command [e]'s preview names, or null when it names none that
/// reads well. Previews are each agent's own short text, so this knows their
/// shapes: Claude's Bash preview is "description: command", and
/// Antigravity's previews are prose ("View a.go").
String? _target(Entry e, RunGroup g, String? agent) {
  if (agent == 'antigravity') return null;
  var t = e.inputPreview?.split('\n').first.trim() ?? '';
  t = t.replaceFirst(_lineCount, '');
  if (g == RunGroup.bash && agent == 'claude') {
    final i = t.indexOf(': ');
    if (i >= 0) t = t.substring(i + 2).trim();
  }
  return t.isEmpty ? null : t;
}

String _name(Entry e, String? agent) {
  final display = toolMeta(agent, e.toolName)?.display ?? '';
  if (display.isNotEmpty) return display;
  final name = e.toolName ?? '';
  return name.isNotEmpty ? name : 'a tool';
}

/// The last path segment of [path].
String _base(String path) {
  final i = path.lastIndexOf('/');
  return _cut(i < 0 || i == path.length - 1 ? path : path.substring(i + 1));
}

/// [s] cut to [_maxTarget] characters, counting an emoji as one so a cut
/// never splits it.
String _cut(String s) {
  final c = s.characters;
  return c.length <= _maxTarget ? s : '${c.take(_maxTarget)}…';
}
