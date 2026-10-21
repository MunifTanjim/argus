import 'dart:math' as math;

import 'package:flutter/material.dart';

import '../state/grouping.dart';
import 'theme.dart';

/// The characters that open the agent's completion menu.
String promptTriggers(String agent) => switch (agent) {
      'claude' || 'antigravity' => '/',
      'codex' => r'/$',
      'opencode' => '/@',
      _ => '',
    };

final _space = RegExp(r'\s');

/// The trigger and the partial name typed at the end of [before]. "/" counts
/// only as the first character of the prompt; the other triggers start any
/// word.
({String trigger, String query})? completionQuery(
    String triggers, String before) {
  if (before.isEmpty) return null;
  if (before.startsWith('/') &&
      triggers.contains('/') &&
      !before.contains(_space)) {
    return (trigger: '/', query: before.substring(1));
  }
  final word = before.substring(before.lastIndexOf(_space) + 1);
  if (word.isEmpty || word.startsWith('/') || !triggers.contains(word[0])) {
    return null;
  }
  return (trigger: word[0], query: word.substring(1));
}

/// Replaces the trailing [trigger] and [query] of [before] with [name].
String applyCompletion(
    String before, String trigger, String query, String name) {
  final typed = trigger + query;
  final head = before.endsWith(typed)
      ? before.substring(0, before.length - typed.length)
      : before;
  return '$head$trigger$name ';
}

const _firstCharMatchBonus = 10;
const _matchFollowingSeparatorBonus = 20;
const _camelCaseMatchBonus = 20;
const _adjacentMatchBonus = 5;
const _unmatchedLeadingCharPenalty = -5;
const _maxUnmatchedLeadingCharPenalty = -15;
final _separators = r'/-_ .\'.runes.toSet();

String _char(int r) => String.fromCharCode(r);
// ASCII skips the String round trips; names are almost always ASCII.
bool _isLower(int r) => r < 0x80
    ? r >= 0x61 && r <= 0x7A
    : _char(r).toUpperCase() != _char(r) && _char(r).toLowerCase() == _char(r);
bool _isUpper(int r) => r < 0x80
    ? r >= 0x41 && r <= 0x5A
    : _char(r).toLowerCase() != _char(r) && _char(r).toUpperCase() == _char(r);
int _asciiLower(int r) => r >= 0x41 && r <= 0x5A ? r | 0x20 : r;
bool _equalFold(int a, int b) {
  if (a == b) return true;
  if (a == 0 || b == 0) return false;
  if (a < 0x80 && b < 0x80) return _asciiLower(a) == _asciiLower(b);
  return _char(a).toLowerCase() == _char(b).toLowerCase();
}
int _utf8Length(int r) => r < 0x80 ? 1 : r < 0x800 ? 2 : r < 0x10000 ? 3 : 4;

/// A port of sahilm/fuzzy's Find, which the TUI ranks with. Scores use UTF-8
/// byte offsets as Go does; the returned hits are UTF-16 offsets.
List<({int index, List<int> hits})> _fuzzyFind(
    String pattern, List<String> data) {
  final runes = pattern.runes.toList();
  if (runes.isEmpty) return const [];
  final found = <({int index, int score, List<int> hits})>[];
  for (var i = 0; i < data.length; i++) {
    var str = data[i];
    final nul = str.indexOf('\u0000');
    if (nul >= 0) str = str.substring(0, nul);
    final chars = str.runes.toList();
    final matched = <int>[];
    final hits = <int>[];
    var total = 0;
    var patternIndex = 0;
    var bestScore = -1;
    var matchedIndex = -1;
    var matchedUnit = -1;
    var currAdjacentMatchBonus = 0;
    var last = 0;
    var lastIndex = 0;
    var j = 0;
    var unit = 0;
    for (var k = 0; k < chars.length; k++) {
      final candidate = chars[k];
      if (_equalFold(candidate, runes[patternIndex])) {
        var score = 0;
        if (j == 0) score += _firstCharMatchBonus;
        if (_isLower(last) && _isUpper(candidate)) {
          score += _camelCaseMatchBonus;
        }
        if (j != 0 && _separators.contains(last)) {
          score += _matchFollowingSeparatorBonus;
        }
        if (matched.isNotEmpty) {
          final bonus = matched.last == lastIndex
              ? currAdjacentMatchBonus * 2 + _adjacentMatchBonus
              : 0;
          score += bonus;
          currAdjacentMatchBonus += bonus;
        }
        if (score > bestScore) {
          bestScore = score;
          matchedIndex = j;
          matchedUnit = unit;
        }
      }
      final nextp =
          patternIndex < runes.length - 1 ? runes[patternIndex + 1] : 0;
      final nextc = k + 1 < chars.length ? chars[k + 1] : 0;
      // Go keeps matchedIndex across pattern characters; so does this port.
      if ((_equalFold(nextp, nextc) || nextc == 0) && matchedIndex > -1) {
        if (matched.isEmpty) {
          bestScore += math.max(matchedIndex * _unmatchedLeadingCharPenalty,
              _maxUnmatchedLeadingCharPenalty);
        }
        total += bestScore;
        matched.add(matchedIndex);
        hits.add(matchedUnit);
        bestScore = -1;
        patternIndex++;
      }
      lastIndex = j;
      last = candidate;
      j += _utf8Length(candidate);
      unit += candidate > 0xFFFF ? 2 : 1;
    }
    total += matched.length - j;
    if (matched.length == runes.length) {
      found.add((index: i, score: total, hits: hits));
    }
  }
  // Go sorts stably by score, highest first.
  found.sort((a, b) =>
      a.score != b.score ? b.score - a.score : a.index - b.index);
  return [for (final f in found) (index: f.index, hits: f.hits)];
}

/// An item and the UTF-16 offsets of its name's matched characters.
typedef PromptMatch = ({PromptCompletion item, List<int> hits});

/// The items whose names match [query], best first, ranked as the TUI ranks
/// them.
List<PromptMatch> matchCompletions(
    List<PromptCompletion> items, String query) {
  final q = query.trim().toLowerCase();
  if (q.isEmpty) return [for (final i in items) (item: i, hits: const [])];
  return [
    for (final f in _fuzzyFind(q, [for (final i in items) i.name]))
      (item: items[f.index], hits: f.hits),
  ];
}

const _rowHeight = 40.0;
const _maxRows = 6;

/// The completion rows, with [selected] highlighted and kept in view.
class PromptMenu extends StatefulWidget {
  const PromptMenu(
      {super.key,
      required this.items,
      required this.selected,
      required this.maxHeight,
      required this.onSelect});

  final List<PromptMatch> items;
  final int selected;
  final double maxHeight;
  final ValueChanged<PromptCompletion> onSelect;

  @override
  State<PromptMenu> createState() => _PromptMenuState();
}

class _PromptMenuState extends State<PromptMenu> {
  final _scroll = ScrollController();

  @override
  void didUpdateWidget(PromptMenu old) {
    super.didUpdateWidget(old);
    if (old.selected != widget.selected && _scroll.hasClients) {
      final top = widget.selected * _rowHeight;
      final view = _scroll.position.viewportDimension;
      if (top < _scroll.offset) {
        _scroll.jumpTo(top);
      } else if (top + _rowHeight > _scroll.offset + view) {
        _scroll.jumpTo(top + _rowHeight - view);
      }
    }
  }

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Material(
        color: AppColors.card,
        elevation: 4,
        shape: RoundedRectangleBorder(
          borderRadius: BorderRadius.circular(4),
          side: const BorderSide(color: AppColors.border),
        ),
        clipBehavior: Clip.antiAlias,
        child: SizedBox(
          height: math.min(math.min(widget.items.length, _maxRows) * _rowHeight,
              widget.maxHeight),
          child: ListView.builder(
            controller: _scroll,
            padding: EdgeInsets.zero,
            itemExtent: _rowHeight,
            itemCount: widget.items.length,
            itemBuilder: (context, i) => _row(widget.items[i], i),
          ),
        ),
      );

  Widget _row(PromptMatch match, int i) {
    final item = match.item;
    return InkWell(
      onTap: () => widget.onSelect(item),
      child: Container(
        color: i == widget.selected ? AppColors.border : null,
        padding: const EdgeInsets.symmetric(horizontal: 12),
        alignment: Alignment.centerLeft,
        child: Row(children: [
          Flexible(
            child: Text.rich(_highlight(item.name, match.hits),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: mono.copyWith(color: AppColors.text)),
          ),
          if (item.description.isNotEmpty) ...[
            const SizedBox(width: 12),
            Expanded(
              child: Text(_firstLine(item.description),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: mono.copyWith(color: AppColors.dim)),
            ),
          ],
        ]),
      ),
    );
  }
}

const _hit = TextStyle(
    fontWeight: FontWeight.bold, decoration: TextDecoration.underline);

/// [name] with the characters at [hits] in bold and underlined, as the TUI
/// marks them.
TextSpan _highlight(String name, List<int> hits) {
  final spans = <TextSpan>[];
  var start = 0;
  bool hit(int at) => hits.contains(at);
  for (var at = 1; at <= name.length; at++) {
    if (at < name.length && name.codeUnitAt(at) & 0xFC00 == 0xDC00) continue;
    if (at == name.length || hit(at) != hit(start)) {
      spans.add(TextSpan(
          text: name.substring(start, at), style: hit(start) ? _hit : null));
      start = at;
    }
  }
  return TextSpan(children: spans);
}

String _firstLine(String s) {
  final nl = s.indexOf('\n');
  return nl < 0 ? s : s.substring(0, nl);
}
