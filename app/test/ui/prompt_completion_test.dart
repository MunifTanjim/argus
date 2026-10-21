import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/state/grouping.dart';
import 'package:argus/ui/prompt_completion.dart';

void main() {
  test('completionQuery', () {
    for (final (triggers, before, want) in <(String, String, (String, String)?)>[
      ('/', '/com', ('/', 'com')),
      ('/', 'fix /com', null),
      ('/', '/compact now', null),
      (r'/$', r'use $sk', (r'$', 'sk')),
      (r'/$', r'a$sk', null),
      ('/@', '@', ('@', '')),
      ('/', '@x', null),
      ('/@', 'line\n@sk', ('@', 'sk')),
      ('/', '/a\nb', null),
      ('', '/com', null),
      (r'/$', r'/tmp/x use $sk', (r'$', 'sk')),
      ('/@', '/compact @sk', ('@', 'sk')),
      ('/@', '/review @', ('@', '')),
      (r'/$', '/tmp/x is broken', null),
      (r'/$', '/tmp/x /co', null),
    ]) {
      final got = completionQuery(triggers, before);
      expect(got == null ? null : (got.trigger, got.query), want,
          reason: 'completionQuery("$triggers", "$before")');
    }
  });

  test('applyCompletion', () {
    expect(applyCompletion(r'use $sk', r'$', 'sk', 'skill-a'), r'use $skill-a ');
    expect(applyCompletion('/', '/', '', 'clear'), '/clear ');
  });

  test('promptTriggers', () {
    for (final MapEntry(key: agent, value: want) in {
      'claude': '/',
      'antigravity': '/',
      'codex': r'/$',
      'opencode': '/@',
      'pi': '',
    }.entries) {
      expect(promptTriggers(agent), want, reason: agent);
    }
  });

  // Expected orders and hit offsets come from sahilm/fuzzy, which the TUI
  // ranks with; the offsets are in UTF-16 code units.
  test('matchCompletions ranks like the TUI', () {
    for (final (names, query, want) in <(List<String>, String, List<(String, List<int>)>)>[
      (['server', 'review'], 'rv', [('review', [0, 2]), ('server', [2, 3])]),
      (['compact', 'clear', 'config', 'cost'], 'c', [('cost', [0]), ('clear', [0]), ('config', [0]), ('compact', [0])]),
      (['compact', 'clear', 'config', 'cost'], 'co', [('cost', [0, 1]), ('config', [0, 1]), ('compact', [0, 1])]),
      (['compact', 'clear', 'config', 'cost'], 'cl', [('clear', [0, 1])]),
      (['compact', 'clear', 'config', 'cost'], 'ct', [('cost', [0, 3]), ('compact', [0, 6])]),
      (['review-pr', 'pr-review', 'preview', 'prune'], 'pr', [('pr-review', [0, 3]), ('prune', [0, 1]), ('preview', [0, 1]), ('review-pr', [7, 8])]),
      (['review-pr', 'pr-review', 'preview', 'prune'], 'rev', [('review-pr', [0, 1, 2]), ('pr-review', [3, 4, 5]), ('preview', [1, 2, 3])]),
      (['review-pr', 'pr-review', 'preview', 'prune'], 'pv', [('preview', [0, 3]), ('pr-review', [0, 5])]),
      (['initCodebase', 'init', 'codebase-init', 'context'], 'ic', [('initCodebase', [0, 4])]),
      (['initCodebase', 'init', 'codebase-init', 'context'], 'cb', [('codebase-init', [0, 4]), ('initCodebase', [4, 8])]),
      (['initCodebase', 'init', 'codebase-init', 'context'], 'Init', [('init', [0, 1, 2, 3]), ('initCodebase', [0, 1, 2, 3]), ('codebase-init', [9, 10, 11, 12])]),
      (['security-review', 'simplify', 'skill_creator', 'statusline'], 'sr', [('security-review', [0, 9]), ('skill_creator', [0, 7])]),
      (['security-review', 'simplify', 'skill_creator', 'statusline'], 'sc', [('skill_creator', [0, 6]), ('security-review', [0, 2])]),
      (['security-review', 'simplify', 'skill_creator', 'statusline'], ' s ', [('simplify', [0]), ('statusline', [0]), ('skill_creator', [0]), ('security-review', [0])]),
      (['xab', 'aab', 'abab'], 'ab', [('abab', [0, 1]), ('aab', [0, 2]), ('xab', [1, 2])]),
      (['The Black Knight'], 'tk', [('The Black Knight', [0, 10])]),
      (['SKILL', 'Skill', 'skill'], 'sk', [('SKILL', [0, 1]), ('Skill', [0, 1]), ('skill', [0, 1])]),
      (['café-au-lait', 'cafe', 'über-éclair'], 'ca', [('café-au-lait', [0, 5]), ('cafe', [0, 1]), ('über-éclair', [6, 8])]),
      (['café-au-lait', 'cafe', 'über-éclair'], 'éa', [('café-au-lait', [3, 5]), ('über-éclair', [5, 8])]),
      (['café-au-lait', 'cafe', 'über-éclair'], 'ue', []),
      (['café-au-lait', 'cafe', 'über-éclair'], 'üe', [('über-éclair', [0, 2])]),
      (['compact', 'clear'], '', [('compact', []), ('clear', [])]),
    ]) {
      final items = [
        for (final n in names)
          PromptCompletion(name: n, description: ''),
      ];
      expect(
          [for (final m in matchCompletions(items, query)) '${m.item.name} ${m.hits}'],
          [for (final (name, hits) in want) '$name $hits'],
          reason: 'matchCompletions($names, "$query")');
    }
  });

  testWidgets('the matched characters are bold', (t) async {
    await t.pumpWidget(MaterialApp(
      home: Scaffold(
        body: PromptMenu(
          items: const [
            (
              item: PromptCompletion(name: 'review', description: ''),
              hits: [0, 2],
            ),
          ],
          selected: 0,
          maxHeight: 400,
          onSelect: (_) {},
        ),
      ),
    ));
    final text = t.widget<RichText>(find.byWidgetPredicate(
        (w) => w is RichText && w.text.toPlainText() == 'review'));
    final bold = <String>[];
    text.text.visitChildren((span) {
      if (span is TextSpan &&
          span.text != null &&
          span.style?.fontWeight == FontWeight.bold) {
        bold.add(span.text!);
      }
      return true;
    });
    expect(bold, ['r', 'v']);
  });
}
