import 'dart:math';

import 'package:flutter/material.dart';

import '../models/entry.dart';
import '../state/tool_detail.dart';
import 'code_block.dart';
import 'item_detail_screen.dart';
import 'item_row.dart';
import 'subagent_trace_screen.dart';
import 'theme.dart';
import 'tool_registry.dart';
import 'usage_format.dart';

const _mono = TextStyle(fontFamily: monoFamily, fontSize: 11, height: 1.3);
TextStyle _monoDim(ArgusTokens t) => _mono.copyWith(color: t.textDim);

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

class EntryRow extends StatelessWidget {
  const EntryRow({
    super.key,
    required this.detailRef,
    required this.entry,
    this.expanded = false,
    this.onToggle,
  });

  final ToolDetailRef detailRef;
  final Entry entry;
  final bool expanded;
  final VoidCallback? onToggle;

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
    final t = context.tokens;
    return Padding(
      padding: const EdgeInsets.only(top: 12, bottom: 4),
      child: Container(
        key: const ValueKey('user-band'),
        constraints: const BoxConstraints(minWidth: double.infinity),
        color: t.surface,
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.person, size: 14, color: t.accent),
                const SizedBox(width: 6),
                Text(
                  'You',
                  style: _mono.copyWith(
                    color: t.text,
                    fontWeight: FontWeight.w600,
                  ),
                ),
                const Spacer(),
                Text(_clockTime(entry.timestamp), style: _monoDim(t)),
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
_ArgusKind _argusKind(String kind, ArgusTokens t) => switch (kind) {
  'text' => (label: 'message', color: t.accent, markdown: true),
  'task' => (label: 'task', color: t.link, markdown: true),
  'reply' => (label: 'reply', color: t.accent, markdown: true),
  'interaction' => (label: 'prompt', color: t.notice, markdown: false),
  'turn_ended' => (label: 'turn ended', color: t.textDim, markdown: false),
  'interaction_ended' => (
    label: 'prompt ended',
    color: t.textDim,
    markdown: false,
  ),
  _ => (label: kind, color: t.textDim, markdown: false),
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
    final t = context.tokens;
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
              child: Text('argus · ${msgs.length} messages', style: _monoDim(t)),
            ),
          for (var i = 0; i < msgs.length; i++)
            _argusMessage(msgs[i], t, first: i == 0),
        ],
      ),
    );
  }

  Widget _argusMessage(ArgusMessage m, ArgusTokens t, {required bool first}) {
    final k = _argusKind(m.kind, t);
    final name = m.child ?? m.from ?? '';
    final session = m.child != null ? m.childSession : m.session;
    return Container(
      key: ValueKey('argus-message:${m.id}'),
      margin: const EdgeInsets.only(bottom: 6),
      padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
      constraints: const BoxConstraints(minWidth: double.infinity),
      decoration: BoxDecoration(
        color: t.surface,
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
                  style: _mono.copyWith(color: t.text),
                ),
              ),
              if (session != null && session.isNotEmpty) ...[
                const SizedBox(width: 6),
                Flexible(
                  child: Text(
                    session,
                    overflow: TextOverflow.ellipsis,
                    style: _monoDim(t),
                  ),
                ),
              ],
              const SizedBox(width: 6),
              Text(m.id, style: _monoDim(t)),
              if (first) ...[
                const Spacer(),
                Text(_clockTime(entry.timestamp), style: _monoDim(t)),
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
    final t = context.tokens;
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
                ? TextStyle(color: t.text, fontSize: 14)
                : _mono.copyWith(color: t.text, fontSize: 12),
          )
        else if (markdown)
          appMarkdown(text)
        else
          Text(
            text,
            style: _mono.copyWith(color: t.text, fontSize: 12),
          ),
        if (long)
          GestureDetector(
            onTap: onToggle,
            child: Padding(
              padding: const EdgeInsets.only(top: 4),
              child: Text(
                expanded ? 'Show less' : 'Show more',
                style: _mono.copyWith(color: t.accent),
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
    final t = context.tokens;
    final text = (entry.text ?? '').trim();
    final row = Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        children: [
          Icon(Icons.lightbulb, size: 14, color: t.textDim),
          const SizedBox(width: 6),
          Text('Thinking', style: _mono.copyWith(color: t.textDim)),
          if (entry.signature) Text(' 🔒', style: _monoDim(t)),
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
            child: Text(text, style: _monoDim(t)),
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
    final t = context.tokens;
    final tc = readableOn(teamBrandColor(tm.color, t), t);
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
    final t = context.tokens;
    final e = entry;
    final parts = <InlineSpan>[];
    // The same fields, in the same order, as the TUI's turn footer.
    void add(String s, {Color? color, IconData? icon, Color? iconColor}) {
      // The no-break space keeps a separator from starting a line.
      if (parts.isNotEmpty) {
        parts.add(TextSpan(text: '\u00A0· ', style: _monoDim(t)));
      }
      final value = TextSpan(
        text: s,
        style: color == null ? _monoDim(t) : _mono.copyWith(color: color),
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
                    color: iconColor ?? t.textDim,
                  ),
                ),
                TextSpan(text: ' ', style: _monoDim(t)),
                value,
              ],
            ),
            softWrap: false,
          ),
        ),
      );
    }

    if (e.interrupted) add('interrupted', color: t.error);
    if (e.modelName?.isNotEmpty ?? false) {
      add(e.modelName!, color: hexColor(e.modelColor) ?? t.textSecondary);
    }
    if (e.thinking > 0) {
      add(
        '${e.thinking}',
        icon: Icons.lightbulb,
        iconColor: t.thinking,
      );
    }
    if (e.toolCount > 0) {
      add('${e.toolCount}', icon: Icons.build, iconColor: t.toolCall);
    }
    if (e.usage.output > 0) add(fmtTokens(e.usage.output), icon: Icons.token);
    if (e.hasContext) add(_fmtContext(e), color: contextColor(e.contextPct, t));
    if (e.durationMs > 0) add(_fmtDuration(e.durationMs), icon: Icons.schedule);
    final time = _clockTime(e.timestamp);
    if (time.isNotEmpty) add(time);

    const lead = 16.0, gap = 8.0;
    final rule = Divider(color: t.line, height: 1);
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 8, horizontal: 4),
      child: parts.isEmpty
          ? rule
          // The stats wrap rather than overflow a narrow screen.
          : LayoutBuilder(
              builder: (context, c) => Row(
                children: [
                  SizedBox(width: lead, child: rule),
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
                  Expanded(child: rule),
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
    final t = context.tokens;
    final c = entry;
    final err = c.isError;
    final hasDetail = c.detail != null && c.detail!.isNotEmpty;

    final header = Row(
      children: [
        Icon(
          Icons.terminal,
          size: 13,
          color: err ? t.error : t.textDim,
        ),
        const SizedBox(width: 6),
        Text(
          title,
          style: _mono.copyWith(
            color: err ? t.error : t.textSecondary,
          ),
        ),
        Text('  ·  ${_clockTime(c.timestamp)}', style: _monoDim(t)),
        if (preview != null && preview!.isNotEmpty)
          Text('  ${preview!}', style: _monoDim(t)),
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
                    style: _mono.copyWith(color: t.text),
                  ),
                ),
              if (hasDetail && expanded)
                Padding(
                  padding: const EdgeInsets.only(top: 6),
                  child: Text(c.detail!, style: _monoDim(t)),
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
    final t = context.tokens;
    final text = (summary == null || summary!.isEmpty)
        ? 'Context compressed'
        : summary!;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 10, horizontal: 4),
      child: Row(
        children: [
          const Expanded(child: Divider(height: 1)),
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 10),
            child: Text(text, style: _monoDim(t)),
          ),
          const Expanded(child: Divider(height: 1)),
        ],
      ),
    );
  }
}
