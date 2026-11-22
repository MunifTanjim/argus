import 'dart:math';

import 'package:flutter/material.dart';

import '../models/entry.dart';
import '../models/pagent.dart' show argusRowText, handoffAnswerOf, offerIdOf, undoCommit;
import '../state/tool_detail.dart';
import 'code_block.dart';
import 'item_detail_screen.dart';
import 'item_row.dart';
import 'subagent_trace_screen.dart';
import 'theme.dart';
import 'tool_registry.dart';
import 'usage_format.dart';

const _mono = TextStyle(fontFamily: 'monospace', fontSize: 11, height: 1.3);
final _monoDim = _mono.copyWith(color: AppColors.dim);

const thinkingCountColor = Color(0xFFd79921); // gruvbox neutral yellow
const toolCountColor = Color(0xFF458588); // gruvbox neutral blue

// Matches the TUI duration format.
String _fmtDuration(int ms) {
  final secs = ms / 1000;
  if (secs >= 60) return '${secs ~/ 60}m ${secs.toInt() % 60}s';
  if (secs >= 10) return '${secs.round()}s';
  return '${secs.toStringAsFixed(1)}s';
}

String _fmtContext(Entry e) {
  if (e.contextDeltaTokens == 0 &&
      e.contextFirstPct.round() == e.contextPct.round()) {
    return fmtContextPct(e.contextPct);
  }
  return 'ctx ${e.contextFirstPct.round()}% → ${e.contextPct.round()}% '
      '(+${fmtTokens(e.contextDeltaTokens)})';
}

String _clockTime(String? ts) {
  if (ts == null) return '';
  final dt = DateTime.tryParse(ts)?.toLocal();
  if (dt == null) return ts;
  String p(int n) => n.toString().padLeft(2, '0');
  return '${p(dt.hour)}:${p(dt.minute)}:${p(dt.second)}';
}

/// Returns null when [e] has nothing deeper to show.
VoidCallback? drillFor(BuildContext context, Entry e, ToolDetailRef ref) {
  if (e.kind == EntryKind.tool ||
      e.kind == EntryKind.skill ||
      isAgentRefTool(e.toolName)) {
    return () => Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => ItemDetailScreen(item: e, detailRef: ref),
      ),
    );
  }
  final sub = e.soleSubagent;
  if (e.kind == EntryKind.subagent &&
      sub != null &&
      !sub.isTeammate &&
      (sub.hasTrace || sub.id.isNotEmpty)) {
    return () => Navigator.of(context).push(
      MaterialPageRoute(
        builder: (_) => SubagentTraceScreen(parentRef: ref, item: e),
      ),
    );
  }
  return null;
}

/// What the user can do with an agent's offers in a thread.
class OfferActions {
  const OfferActions({
    required this.isOpen,
    required this.onStart,
    required this.onDismiss,
  });

  /// Whether the offer is still waiting for an answer.
  final bool Function(String offerId) isOpen;
  final void Function(String offerId) onStart;
  final void Function(String offerId) onDismiss;
}

class EntryRow extends StatelessWidget {
  const EntryRow({
    super.key,
    required this.detailRef,
    required this.entry,
    this.expanded = false,
    this.onToggle,
    this.onUndoMemory,
    this.offerActions,
  });

  final ToolDetailRef detailRef;
  final Entry entry;
  final bool expanded;
  final VoidCallback? onToggle;

  /// Offers an undo on an argus entry that reports a memory change; null
  /// leaves the entry without one.
  final void Function(String commit)? onUndoMemory;

  /// Offers Start and Dismiss on an agent's open offers; null leaves offers
  /// as plain text.
  final OfferActions? offerActions;

  @override
  Widget build(BuildContext context) {
    final e = entry;
    switch (e.kind) {
      case EntryKind.user:
        if (e.argusMessages.isNotEmpty) {
          return _ArgusCard(entry: e, expanded: expanded, onToggle: onToggle);
        }
        return _UserBand(entry: e, expanded: expanded, onToggle: onToggle);
      case EntryKind.text:
        return Padding(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: appMarkdown(e.text ?? ''),
        );
      case EntryKind.thinking:
        return _ThinkingRow(entry: e, expanded: expanded, onToggle: onToggle);
      case EntryKind.turnEnd:
        return _TurnEndDivider(entry: e);
      case EntryKind.system:
        return _EventCard(
          title: 'System',
          entry: e,
          expanded: expanded,
          onToggle: onToggle,
          preview: e.label,
        );
      case EntryKind.shell:
        return _EventCard(
          title: 'Shell',
          entry: e,
          expanded: expanded,
          onToggle: onToggle,
          command: e.text ?? '',
        );
      case EntryKind.compact:
        return _CompactDivider(summary: e.summary);
      case EntryKind.subagent
          when e.isTeammate && !(e.soleSubagent?.idle ?? false):
        return _TeammateMessage(
          entry: e,
          expanded: expanded,
          onToggle: onToggle,
        );
      case EntryKind.tool:
      case EntryKind.skill:
      case EntryKind.subagent:
        return ItemRow(
          item: e,
          agent: detailRef.agent,
          onTap: drillFor(context, e, detailRef),
        );
      case EntryKind.argus:
        return _ArgusEventRow(
          entry: e,
          onUndo: onUndoMemory,
          offers: offerActions,
        );
      case EntryKind.unknown:
        return const SizedBox.shrink();
    }
  }
}

class _UserBand extends StatelessWidget {
  const _UserBand({
    required this.entry,
    required this.expanded,
    required this.onToggle,
  });
  final Entry entry;
  final bool expanded;
  final VoidCallback? onToggle;
  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.only(top: 12, bottom: 4),
      child: Container(
        key: const ValueKey('user-band'),
        constraints: const BoxConstraints(minWidth: double.infinity),
        color: AppColors.card,
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                const Icon(Icons.person, size: 14, color: AppColors.accent),
                const SizedBox(width: 6),
                Text(
                  'You',
                  style: _mono.copyWith(
                    color: AppColors.text,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const Spacer(),
                Text(_clockTime(entry.timestamp), style: _monoDim),
              ],
            ),
            const SizedBox(height: 4),
            _Foldable(
              text: entry.text ?? '',
              expanded: expanded,
              onToggle: onToggle,
            ),
          ],
        ),
      ),
    );
  }
}

typedef _ArgusKind = ({String label, Color color, bool markdown});

/// Follows the argus-ui Claude Code plugin: messages draw their body as
/// markdown, notices keep their lines (tool input) as written.
_ArgusKind _argusKind(String kind) => switch (kind) {
  'text' => (label: 'message', color: AppColors.accent, markdown: true),
  'task' => (label: 'task', color: AppColors.link, markdown: true),
  'reply' => (label: 'reply', color: AppColors.accent, markdown: true),
  'interaction' => (label: 'prompt', color: AppColors.notice, markdown: false),
  'turn_ended' => (label: 'turn ended', color: AppColors.dim, markdown: false),
  'interaction_ended' => (
    label: 'prompt ended',
    color: AppColors.dim,
    markdown: false,
  ),
  _ => (label: kind, color: AppColors.dim, markdown: false),
};

/// A user entry whose prompt argus delivered: one framed card per message.
class _ArgusCard extends StatelessWidget {
  const _ArgusCard({
    required this.entry,
    required this.expanded,
    required this.onToggle,
  });
  final Entry entry;
  final bool expanded;
  final VoidCallback? onToggle;
  @override
  Widget build(BuildContext context) {
    final msgs = entry.argusMessages;
    return Padding(
      key: const ValueKey('argus-card'),
      padding: const EdgeInsets.only(top: 12, bottom: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          if (msgs.length > 1)
            Padding(
              padding: const EdgeInsets.only(bottom: 4),
              child: Text('argus · ${msgs.length} messages', style: _monoDim),
            ),
          for (var i = 0; i < msgs.length; i++)
            _argusMessage(msgs[i], first: i == 0),
        ],
      ),
    );
  }

  Widget _argusMessage(ArgusMessage m, {required bool first}) {
    final k = _argusKind(m.kind);
    final name = m.child ?? m.from ?? '';
    final session = m.child != null ? m.childSession : m.session;
    return Container(
      key: ValueKey('argus-message:${m.id}'),
      margin: const EdgeInsets.only(bottom: 6),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      constraints: const BoxConstraints(minWidth: double.infinity),
      decoration: BoxDecoration(
        color: AppColors.card,
        border: Border.all(color: k.color),
        borderRadius: BorderRadius.circular(6),
      ),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(
                k.label,
                style: _mono.copyWith(
                  color: k.color,
                  fontWeight: FontWeight.w600,
                ),
              ),
              const SizedBox(width: 6),
              Flexible(
                child: Text(
                  name,
                  overflow: TextOverflow.ellipsis,
                  style: _mono.copyWith(color: AppColors.text),
                ),
              ),
              if (session != null && session.isNotEmpty) ...[
                const SizedBox(width: 6),
                Flexible(
                  child: Text(
                    session,
                    overflow: TextOverflow.ellipsis,
                    style: _monoDim,
                  ),
                ),
              ],
              const SizedBox(width: 6),
              Text(m.id, style: _monoDim),
              if (first) ...[
                const Spacer(),
                Text(_clockTime(entry.timestamp), style: _monoDim),
              ],
            ],
          ),
          const SizedBox(height: 4),
          _Foldable(
            text: m.body,
            expanded: expanded,
            onToggle: onToggle,
            markdown: k.markdown,
          ),
        ],
      ),
    );
  }
}

/// [text] that folds to its first lines behind Show more / Show less when it
/// is long; drawn as markdown, or as plain monospace lines.
class _Foldable extends StatelessWidget {
  const _Foldable({
    required this.text,
    required this.expanded,
    required this.onToggle,
    this.markdown = true,
  });
  final String text;
  final bool expanded;
  final VoidCallback? onToggle;
  final bool markdown;
  static const _maxLines = 10;
  @override
  Widget build(BuildContext context) {
    final lineCount = '\n'.allMatches(text).length + 1;
    final long = lineCount > _maxLines || text.length > 600;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (long && !expanded)
          Text(
            text.split('\n').take(_maxLines).join('\n'),
            maxLines: _maxLines,
            overflow: TextOverflow.ellipsis,
            style: markdown
                ? const TextStyle(color: AppColors.text, fontSize: 14)
                : _mono.copyWith(color: AppColors.text, fontSize: 12),
          )
        else if (markdown)
          appMarkdown(text)
        else
          Text(
            text,
            style: _mono.copyWith(color: AppColors.text, fontSize: 12),
          ),
        if (long)
          GestureDetector(
            onTap: onToggle,
            child: Padding(
              padding: const EdgeInsets.only(top: 4),
              child: Text(
                expanded ? 'Show less' : 'Show more',
                style: _mono.copyWith(color: AppColors.accent),
              ),
            ),
          ),
      ],
    );
  }
}

class _ThinkingRow extends StatelessWidget {
  const _ThinkingRow({
    required this.entry,
    required this.expanded,
    required this.onToggle,
  });
  final Entry entry;
  final bool expanded;
  final VoidCallback? onToggle;
  @override
  Widget build(BuildContext context) {
    final text = (entry.text ?? '').trim();
    final row = Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        children: [
          const Icon(Icons.lightbulb, size: 14, color: AppColors.dim),
          const SizedBox(width: 6),
          Text('Thinking', style: _mono.copyWith(color: AppColors.dim)),
          if (entry.signature) Text(' 🔒', style: _monoDim),
        ],
      ),
    );
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        if (text.isEmpty) row else InkWell(onTap: onToggle, child: row),
        if (expanded)
          Padding(
            padding: const EdgeInsets.only(left: 20, bottom: 4),
            child: Text(text, style: _monoDim),
          ),
      ],
    );
  }
}

class _TeammateMessage extends StatelessWidget {
  const _TeammateMessage({
    required this.entry,
    required this.expanded,
    required this.onToggle,
  });
  final Entry entry;
  final bool expanded;
  final VoidCallback? onToggle;

  @override
  Widget build(BuildContext context) {
    final tm = entry.soleSubagent!;
    final tc = teamColor(tm.color);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Icon(Icons.forum_outlined, size: 14, color: tc),
              const SizedBox(width: 6),
              Text(
                tm.name.isNotEmpty ? tm.name : 'teammate',
                style: _mono.copyWith(color: tc, fontWeight: FontWeight.w600),
              ),
            ],
          ),
          if ((entry.text ?? '').trim().isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 4),
              child: _Foldable(
                text: entry.text!,
                expanded: expanded,
                onToggle: onToggle,
              ),
            ),
        ],
      ),
    );
  }
}

class _TurnEndDivider extends StatelessWidget {
  const _TurnEndDivider({required this.entry});
  final Entry entry;

  @override
  Widget build(BuildContext context) {
    final e = entry;
    final parts = <InlineSpan>[];
    // The same fields, in the same order, as the TUI's turn footer.
    void add(String s, {Color? color, IconData? icon, Color? iconColor}) {
      // The no-break space keeps a separator from starting a line.
      if (parts.isNotEmpty) {
        parts.add(TextSpan(text: '\u00A0· ', style: _monoDim));
      }
      final value = TextSpan(
        text: s,
        style: color == null ? _monoDim : _mono.copyWith(color: color),
      );
      if (icon == null) {
        parts.add(value);
        return;
      }
      // One unit, so a line break cannot split the icon from its value.
      parts.add(
        WidgetSpan(
          alignment: PlaceholderAlignment.baseline,
          baseline: TextBaseline.alphabetic,
          child: Text.rich(
            TextSpan(
              children: [
                WidgetSpan(
                  alignment: PlaceholderAlignment.middle,
                  child: Icon(
                    icon,
                    size: 12,
                    color: iconColor ?? AppColors.dim,
                  ),
                ),
                TextSpan(text: ' ', style: _monoDim),
                value,
              ],
            ),
            softWrap: false,
          ),
        ),
      );
    }

    if (e.interrupted) add('interrupted', color: AppColors.error);
    if (e.modelName?.isNotEmpty ?? false) {
      add(e.modelName!, color: hexColor(e.modelColor) ?? AppColors.secondary);
    }
    if (e.thinking > 0) {
      add(
        '${e.thinking}',
        icon: Icons.lightbulb,
        iconColor: thinkingCountColor,
      );
    }
    if (e.toolCount > 0) {
      add('${e.toolCount}', icon: Icons.build, iconColor: toolCountColor);
    }
    if (e.usage.output > 0) add(fmtTokens(e.usage.output), icon: Icons.token);
    if (e.hasContext) add(_fmtContext(e), color: contextColor(e.contextPct));
    if (e.durationMs > 0) add(_fmtDuration(e.durationMs), icon: Icons.schedule);
    final time = _clockTime(e.timestamp);
    if (time.isNotEmpty) add(time);

    const lead = 16.0, gap = 8.0;
    const rule = Divider(color: AppColors.border, height: 1);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 4),
      child: parts.isEmpty
          ? rule
          // The stats wrap rather than overflow a narrow screen.
          : LayoutBuilder(
              builder: (context, c) => Row(
                children: [
                  const SizedBox(width: lead, child: rule),
                  const SizedBox(width: gap),
                  ConstrainedBox(
                    constraints: BoxConstraints(
                      maxWidth: max(0, c.maxWidth - 2 * (lead + gap)),
                    ),
                    child: Text.rich(
                      TextSpan(children: parts),
                      textAlign: TextAlign.center,
                    ),
                  ),
                  const SizedBox(width: gap),
                  const Expanded(child: rule),
                ],
              ),
            ),
    );
  }
}

/// A system or shell event: a header that expands to its detail. [command]
/// is the shell command line; [preview] follows the timestamp (e.g. "Recap").
class _EventCard extends StatelessWidget {
  const _EventCard({
    required this.title,
    required this.entry,
    required this.expanded,
    required this.onToggle,
    this.preview,
    this.command,
  });
  final String title;
  final Entry entry;
  final bool expanded;
  final VoidCallback? onToggle;
  final String? preview;
  final String? command;
  @override
  Widget build(BuildContext context) {
    final c = entry;
    final err = c.isError;
    final hasDetail = c.detail != null && c.detail!.isNotEmpty;

    final header = Row(
      children: [
        Icon(
          Icons.terminal,
          size: 13,
          color: err ? AppColors.error : AppColors.dim,
        ),
        const SizedBox(width: 6),
        Text(
          title,
          style: _mono.copyWith(
            color: err ? AppColors.error : AppColors.secondary,
          ),
        ),
        Text('  ·  ${_clockTime(c.timestamp)}', style: _monoDim),
        if (preview != null && preview!.isNotEmpty)
          Text('  ${preview!}', style: _monoDim),
      ],
    );

    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: InkWell(
        onTap: hasDetail ? onToggle : null,
        borderRadius: BorderRadius.circular(6),
        child: Container(
          width: double.infinity,
          padding: const EdgeInsets.all(12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              header,
              if (command != null)
                Padding(
                  padding: const EdgeInsets.only(top: 6),
                  child: Text(
                    '\$ ${command!}',
                    style: _mono.copyWith(color: AppColors.text),
                  ),
                ),
              if (hasDetail && expanded)
                Padding(
                  padding: const EdgeInsets.only(top: 6),
                  child: Text(c.detail!, style: _monoDim),
                ),
            ],
          ),
        ),
      ),
    );
  }
}

class _CompactDivider extends StatelessWidget {
  const _CompactDivider({required this.summary});
  final String? summary;

  @override
  Widget build(BuildContext context) {
    final text = (summary == null || summary!.isEmpty)
        ? 'Context compressed'
        : summary!;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 10, horizontal: 4),
      child: Row(
        children: [
          const Expanded(child: Divider(color: AppColors.border, height: 1)),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 10),
            child: Text(text, style: _monoDim),
          ),
          const Expanded(child: Divider(color: AppColors.border, height: 1)),
        ],
      ),
    );
  }
}

/// Argus's own event in an agent thread: dim, with an undo on a memory or
/// routine change, and Start and Dismiss on an open offer.
class _ArgusEventRow extends StatelessWidget {
  const _ArgusEventRow({required this.entry, this.onUndo, this.offers});
  final Entry entry;
  final void Function(String commit)? onUndo;
  final OfferActions? offers;

  @override
  Widget build(BuildContext context) {
    final e = entry;
    final answer = handoffAnswerOf(e);
    if (answer != null) return _HandoffAnswer(entry: e, answer: answer);
    final kind = e.event?.kind;
    final text = argusRowText(e);
    final commit = undoCommit(e);
    final offerId = offerIdOf(e);
    final open = offerId.isNotEmpty && (offers?.isOpen(offerId) ?? false);
    // A brief is the point of its event, so it shows whole.
    final lines = kind == 'brief' ? null : 2;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            key: const ValueKey('argus-event'),
            children: [
              const Icon(Icons.terminal, size: 13, color: AppColors.dim),
              const SizedBox(width: 6),
              Text('Argus', style: _mono.copyWith(color: AppColors.secondary)),
              Text('  ·  ${_clockTime(e.timestamp)}', style: _monoDim),
              if (text.isNotEmpty && kind != 'brief')
                Flexible(
                  child: Text(
                    '  $text',
                    style: _monoDim,
                    maxLines: lines,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
              if (commit.isNotEmpty && onUndo != null)
                TextButton(
                  onPressed: () => onUndo!(commit),
                  style: TextButton.styleFrom(
                    visualDensity: VisualDensity.compact,
                    minimumSize: const Size(0, 28),
                  ),
                  child: const Text('Undo'),
                ),
            ],
          ),
          if (kind == 'brief' && text.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(top: 4, left: 19),
              child: appMarkdown(text),
            ),
          if (open)
            Padding(
              padding: const EdgeInsets.only(left: 13),
              child: Row(
                children: [
                  FilledButton(
                    onPressed: () => offers!.onStart(offerId),
                    style: FilledButton.styleFrom(
                      visualDensity: VisualDensity.compact,
                    ),
                    child: const Text('Start'),
                  ),
                  const SizedBox(width: 8),
                  TextButton(
                    onPressed: () => offers!.onDismiss(offerId),
                    child: const Text('Dismiss'),
                  ),
                ],
              ),
            ),
        ],
      ),
    );
  }
}

/// Another agent's answer to a hand-off: its name, then the answer in full.
class _HandoffAnswer extends StatelessWidget {
  const _HandoffAnswer({required this.entry, required this.answer});
  final Entry entry;
  final ({String agent, String text}) answer;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 4),
    child: Column(
      key: const ValueKey('handoff-answer'),
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        Row(
          children: [
            const Icon(Icons.reply, size: 13, color: AppColors.accent),
            const SizedBox(width: 6),
            Text(
              answer.agent,
              style: _mono.copyWith(
                color: AppColors.text,
                fontWeight: FontWeight.w600,
              ),
            ),
            Text('  ·  answered  ·  ${_clockTime(entry.timestamp)}', style: _monoDim),
          ],
        ),
        if (answer.text.trim().isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 4, left: 19),
            child: appMarkdown(answer.text),
          ),
      ],
    ),
  );
}
