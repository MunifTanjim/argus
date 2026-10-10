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

const _mono = TextStyle(fontFamily: 'monospace', fontSize: 11, height: 1.3);
final _monoDim = _mono.copyWith(color: AppColors.dim);

/// Mirrors the TUI thresholds. A healthy context stays dim so it doesn't
/// compete with the elevated states.
Color _ctxColor(double pct) {
  if (pct >= 80) return const Color(0xFFfb4934); // red
  if (pct >= 50) return const Color(0xFFfabd2f); // yellow
  return AppColors.dim;
}

Color? _hexColor(String? hex) {
  if (hex == null || hex.length != 7 || !hex.startsWith('#')) return null;
  final v = int.tryParse(hex.substring(1), radix: 16);
  return v == null ? null : Color(0xFF000000 | v);
}

const thinkingCountColor = Color(0xFFd79921); // gruvbox neutral yellow
const toolCountColor = Color(0xFF458588); // gruvbox neutral blue

String _fmtTokens(int n) =>
    n >= 1000 ? '${(n / 1000).toStringAsFixed(1)}k' : '$n';

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
    return 'ctx ${e.contextPct.round()}%';
  }
  return 'ctx ${e.contextFirstPct.round()}% → ${e.contextPct.round()}% '
      '(+${_fmtTokens(e.contextDeltaTokens)})';
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
        return _TeammateMessage(entry: e);
      case EntryKind.tool:
      case EntryKind.skill:
      case EntryKind.subagent:
        return ItemRow(
            item: e,
            agent: detailRef.agent,
            onTap: drillFor(context, e, detailRef));
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
  static const _maxLines = 10;
  @override
  Widget build(BuildContext context) {
    final text = entry.text ?? '';
    final lineCount = '\n'.allMatches(text).length + 1;
    final long = lineCount > _maxLines || text.length > 600;
    final Widget body;
    if (long && !expanded) {
      body = Text(
        text.split('\n').take(_maxLines).join('\n'),
        maxLines: _maxLines,
        overflow: TextOverflow.ellipsis,
        style: const TextStyle(color: AppColors.text, fontSize: 14),
      );
    } else {
      body = appMarkdown(text);
    }
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
            body,
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
        ),
      ),
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
  const _TeammateMessage({required this.entry});
  final Entry entry;

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
              child: appMarkdown(entry.text!),
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
                  child: Icon(icon, size: 12, color: iconColor ?? AppColors.dim),
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
      add(e.modelName!, color: _hexColor(e.modelColor) ?? AppColors.secondary);
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
    if (e.usage.output > 0) add(_fmtTokens(e.usage.output), icon: Icons.token);
    if (e.hasContext) add(_fmtContext(e), color: _ctxColor(e.contextPct));
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
