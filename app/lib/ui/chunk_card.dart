import 'package:flutter/material.dart';

import '../models/chunk.dart';
import '../state/tool_detail.dart';
import 'code_block.dart';
import 'item_detail_screen.dart';
import 'item_row.dart';
import 'subagent_trace_screen.dart';
import 'theme.dart';
import 'tool_registry.dart';

const _mono = TextStyle(fontFamily: 'monospace', fontSize: 11, height: 1.3);
final _monoDim = _mono.copyWith(color: AppColors.dim);

/// Context-pressure color, mirroring the TUI thresholds (50/80). A healthy
/// context stays dim so it doesn't compete with the elevated states.
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

String _fmtTokens(int n) =>
    n >= 1000 ? '${(n / 1000).toStringAsFixed(1)}k' : '$n';

// RFC3339 -> local HH:MM:SS.
String _clockTime(String? ts) {
  if (ts == null) return '';
  final dt = DateTime.tryParse(ts)?.toLocal();
  if (dt == null) return ts;
  String p(int n) => n.toString().padLeft(2, '0');
  return '${p(dt.hour)}:${p(dt.minute)}:${p(dt.second)}';
}

class ChunkCard extends StatefulWidget {
  const ChunkCard({super.key, required this.detailRef, required this.chunk});

  final ToolDetailRef detailRef;
  final Chunk chunk;

  @override
  State<ChunkCard> createState() => _ChunkCardState();
}

class _ChunkCardState extends State<ChunkCard> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    switch (widget.chunk.kind) {
      case ChunkKind.user:
        return _UserBubble(text: widget.chunk.text ?? '');
      case ChunkKind.ai:
        return _aiCard(widget.chunk);
      case ChunkKind.system:
        return _SystemCard(chunk: widget.chunk);
      case ChunkKind.shell:
        return _ShellCard(chunk: widget.chunk);
      case ChunkKind.skill:
        return _SkillCard(chunk: widget.chunk);
      case ChunkKind.compact:
        return _CompactDivider(summary: widget.chunk.summary);
      case ChunkKind.unknown:
        return const SizedBox.shrink();
    }
  }

  Widget _aiCard(Chunk c) {
    // The header (chevron + meta) is the only collapse target. Keeping the toggle
    // off the body means scrolling or drilling inside an expanded card can't
    // accidentally collapse it.
    final header = InkWell(
      borderRadius: BorderRadius.circular(4),
      onTap: () => setState(() => _expanded = !_expanded),
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 2),
        child: Row(
          children: [
            Icon(_expanded ? Icons.expand_more : Icons.chevron_right,
                size: 16, color: AppColors.dim),
            const SizedBox(width: 4),
            Expanded(child: _metaLine(c)),
          ],
        ),
      ),
    );

    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Material(
        color: Colors.transparent,
        child: Container(
          decoration: BoxDecoration(
            color: AppColors.card,
            border: Border.all(color: AppColors.border),
            borderRadius: BorderRadius.circular(6),
          ),
          padding: const EdgeInsets.all(12),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              header,
              const SizedBox(height: 6),
              // Expanded body is free of any toggle gesture; tool/subagent rows
              // keep their own drill handlers. Collapsed: tap the short preview
              // (no scroll-collapse risk) to expand.
              if (_expanded)
                ..._expandedBody(c)
              else
                InkWell(
                  borderRadius: BorderRadius.circular(4),
                  onTap: () => setState(() => _expanded = true),
                  child: _collapsedBody(c),
                ),
            ],
          ),
        ),
      ),
    );
  }

  /// AI header: a colored model anchor + activity glyphs on the left, perf
  /// metrics (tokens · ctx% · duration) right-aligned. Model color and the
  /// pressure-colored ctx% give the eye anchors; everything else stays dim.
  Widget _metaLine(Chunk c) {
    final left = <Widget>[];
    if (c.modelName?.isNotEmpty ?? false) {
      left.add(Flexible(
        child: Text(c.modelName!,
            overflow: TextOverflow.ellipsis,
            style: _mono.copyWith(
                color: _hexColor(c.modelColor) ?? AppColors.secondary)),
      ));
    }
    if (c.thinking > 0) left.add(_metaCount(Icons.lightbulb, c.thinking));
    if (c.toolCount > 0) left.add(_metaCount(Icons.build, c.toolCount));

    final right = <Widget>[];
    if (c.usage.total > 0) right.add(Text(_fmtTokens(c.usage.total), style: _monoDim));
    if (c.hasContext) {
      right.add(Text('${c.contextPct.round()}%',
          style: _mono.copyWith(color: _ctxColor(c.contextPct))));
    }
    if (c.durationMs > 0) {
      right.add(Text('${(c.durationMs / 1000).toStringAsFixed(1)}s', style: _monoDim));
    }

    if (left.isEmpty && right.isEmpty) return Text('response', style: _monoDim);

    return Row(
      children: [
        Expanded(child: _spaced(left, 10)),
        if (right.isNotEmpty) ...[
          const SizedBox(width: 12),
          _spaced(right, 10),
        ],
      ],
    );
  }

  // Icon + count, e.g. thinking/tool tallies.
  Widget _metaCount(IconData icon, int n) => Row(
        mainAxisSize: MainAxisSize.min,
        children: [
          Icon(icon, size: 12, color: AppColors.dim),
          const SizedBox(width: 3),
          Text('$n', style: _monoDim),
        ],
      );

  // A min-width Row with uniform gaps between items.
  Widget _spaced(List<Widget> items, double gap) {
    final row = <Widget>[];
    for (var i = 0; i < items.length; i++) {
      if (i > 0) row.add(SizedBox(width: gap));
      row.add(items[i]);
    }
    return Row(mainAxisSize: MainAxisSize.min, children: row);
  }

  Widget _collapsedBody(Chunk c) {
    final last = c.previewItem;
    final extra = c.items.length > 1
        ? Padding(
            padding: const EdgeInsets.only(top: 4),
            child: Text('▸ ${c.items.length} items',
                style: _monoDim),
          )
        : const SizedBox.shrink();

    if (last == null) {
      return Text('(no output)', style: _monoDim);
    }
    if (last.kind == ItemKind.text) {
      final lines = (last.text ?? '')
          .trim()
          .split('\n')
          .where((l) => l.trim().isNotEmpty)
          .toList();
      final firstLine = lines.isEmpty ? '' : lines.first;
      final hidden = lines.length - 1;
      return Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(firstLine,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(color: AppColors.text, fontSize: 14)),
          // A single long text item gets a line-count hint; multi-item chunks
          // already show the "N items" count via [extra].
          if (hidden > 0 && c.items.length <= 1)
            Padding(
              padding: const EdgeInsets.only(top: 4),
              child: Text('… +$hidden more lines', style: _monoDim),
            ),
          extra,
        ],
      );
    }
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [ItemRow(item: last), extra],
    );
  }

  List<Widget> _expandedBody(Chunk c) {
    final widgets = <Widget>[];
    for (final it in c.items) {
      if (it.kind == ItemKind.text) {
        if ((it.text ?? '').trim().isEmpty) continue;
        widgets.add(Padding(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: appMarkdown(it.text!),
        ));
      } else if (it.isTeammate && !(it.soleSubagent?.idle ?? false)) {
        // Attributed header (team color) + full message body.
        final tm = it.soleSubagent!;
        final tc = teamColor(tm.color);
        widgets.add(Padding(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Row(children: [
                Icon(Icons.forum_outlined, size: 14, color: tc),
                const SizedBox(width: 6),
                Text(tm.name.isNotEmpty ? tm.name : 'teammate',
                    style: _mono.copyWith(
                        color: tc, fontWeight: FontWeight.w600)),
              ]),
              if ((it.text ?? '').trim().isNotEmpty)
                Padding(
                  padding: const EdgeInsets.only(top: 4),
                  child: appMarkdown(it.text!),
                ),
            ],
          ),
        ));
      } else {
        widgets.add(ItemRow(item: it, onTap: _drill(it)));
      }
    }
    if (widgets.isEmpty) {
      widgets.add(
          Text('(no output)', style: _monoDim));
    }
    return widgets;
  }

  /// Returns a tap handler for drillable rows, or null for non-drillable ones.
  /// Tool rows open the tool detail; subagent rows with a trace (inline or
  /// streamable via agentId) open the subagent trace.
  VoidCallback? _drill(Item it) {
    // Thinking only drills when it has text to show.
    if (it.kind == ItemKind.tool ||
        it.kind == ItemKind.skill ||
        isAgentRefTool(it.toolName) ||
        (it.kind == ItemKind.thinking && (it.text ?? '').trim().isNotEmpty)) {
      return () => Navigator.of(context).push(
            MaterialPageRoute(
              builder: (_) =>
                  ItemDetailScreen(item: it, detailRef: widget.detailRef),
            ),
          );
    }
    final sub = it.soleSubagent;
    if (it.kind == ItemKind.subagent &&
        sub != null &&
        (sub.hasTrace || sub.id.isNotEmpty)) {
      return () => Navigator.of(context).push(
            MaterialPageRoute(
              builder: (_) =>
                  SubagentTraceScreen(parentRef: widget.detailRef, item: it),
            ),
          );
    }
    return null;
  }
}

class _UserBubble extends StatefulWidget {
  const _UserBubble({required this.text});
  final String text;

  @override
  State<_UserBubble> createState() => _UserBubbleState();
}

class _UserBubbleState extends State<_UserBubble> {
  static const _maxLines = 10;
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final text = widget.text;
    final lineCount = '\n'.allMatches(text).length + 1;
    final long = lineCount > _maxLines || text.length > 600;

    final Widget body;
    if (long && !_expanded) {
      // A plain-text preview keeps the collapsed height bounded; the full
      // markdown renders once expanded.
      final preview = text.split('\n').take(_maxLines).join('\n');
      body = Column(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [
          Text(preview,
              maxLines: _maxLines,
              overflow: TextOverflow.ellipsis,
              style: const TextStyle(color: AppColors.text, fontSize: 14)),
          _toggle('Show more'),
        ],
      );
    } else if (long) {
      body = Column(
        crossAxisAlignment: CrossAxisAlignment.end,
        children: [appMarkdown(text), _toggle('Show less')],
      );
    } else {
      body = appMarkdown(text);
    }

    return Padding(
      padding: const EdgeInsets.only(bottom: 8, left: 40),
      child: Align(
        alignment: Alignment.centerRight,
        child: Container(
          decoration: BoxDecoration(
            color: AppColors.card,
            border: Border.all(color: AppColors.accent.withValues(alpha: 0.5)),
            borderRadius: BorderRadius.circular(6),
          ),
          padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
          child: body,
        ),
      ),
    );
  }

  Widget _toggle(String label) => GestureDetector(
        onTap: () => setState(() => _expanded = !_expanded),
        child: Padding(
          padding: const EdgeInsets.only(top: 4),
          child: Text(label,
              style: _mono.copyWith(color: AppColors.accent, fontSize: 11)),
        ),
      );
}

class _SystemCard extends StatefulWidget {
  const _SystemCard({required this.chunk});
  final Chunk chunk;

  @override
  State<_SystemCard> createState() => _SystemCardState();
}

class _SystemCardState extends State<_SystemCard> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final c = widget.chunk;
    final err = c.isError;
    final labelColor = err ? AppColors.error : AppColors.secondary;
    final hasDetail = c.detail != null && c.detail!.isNotEmpty;
    final hasLabel = c.label != null && c.label!.isNotEmpty;

    final header = Row(
      children: [
        Icon(Icons.terminal, size: 13, color: err ? AppColors.error : AppColors.dim),
        const SizedBox(width: 6),
        Text('System', style: _mono.copyWith(color: labelColor)),
        Text('  ·  ${_clockTime(c.timestamp)}', style: _monoDim),
        if (hasLabel) // preview after the timestamp (e.g. "Recap")
          Text('  ${c.label!}', style: _monoDim),
      ],
    );

    return Padding(
      padding: const EdgeInsets.only(bottom: 8), // match the AI card's edges
      child: InkWell(
        onTap: hasDetail ? () => setState(() => _expanded = !_expanded) : null,
        borderRadius: BorderRadius.circular(6),
        child: Container(
          width: double.infinity,
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: AppColors.card,
            border: Border.all(color: AppColors.border),
            borderRadius: BorderRadius.circular(6),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              header,
              if (hasDetail && _expanded)
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

class _ShellCard extends StatefulWidget {
  const _ShellCard({required this.chunk});
  final Chunk chunk;

  @override
  State<_ShellCard> createState() => _ShellCardState();
}

class _ShellCardState extends State<_ShellCard> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final c = widget.chunk;
    final err = c.isError;
    final hasDetail = c.detail != null && c.detail!.isNotEmpty;

    final header = Row(
      children: [
        Icon(Icons.terminal,
            size: 13, color: err ? AppColors.error : AppColors.dim),
        const SizedBox(width: 6),
        Text('Shell',
            style: _mono.copyWith(
                color: err ? AppColors.error : AppColors.secondary)),
        Text('  ·  ${_clockTime(c.timestamp)}', style: _monoDim),
      ],
    );

    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: InkWell(
        onTap: hasDetail ? () => setState(() => _expanded = !_expanded) : null,
        borderRadius: BorderRadius.circular(6),
        child: Container(
          width: double.infinity,
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: AppColors.card,
            border: Border.all(color: AppColors.border),
            borderRadius: BorderRadius.circular(6),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              header,
              Padding(
                padding: const EdgeInsets.only(top: 6),
                child: Text('\$ ${c.text ?? ''}',
                    style: _mono.copyWith(color: AppColors.text)),
              ),
              if (hasDetail && _expanded)
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

class _SkillCard extends StatefulWidget {
  const _SkillCard({required this.chunk});
  final Chunk chunk;

  @override
  State<_SkillCard> createState() => _SkillCardState();
}

class _SkillCardState extends State<_SkillCard> {
  bool _expanded = false;

  @override
  Widget build(BuildContext context) {
    final c = widget.chunk;
    final hasPath = c.label != null && c.label!.isNotEmpty;
    final hasBody = c.detail != null && c.detail!.isNotEmpty;
    final expandable = hasPath || hasBody;

    final header = Row(
      children: [
        const Icon(Icons.school_outlined, size: 13, color: AppColors.dim),
        const SizedBox(width: 6),
        Text('Skill', style: _mono.copyWith(color: AppColors.secondary)),
        Text('  ·  ${_clockTime(c.timestamp)}', style: _monoDim),
      ],
    );

    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: InkWell(
        onTap: expandable ? () => setState(() => _expanded = !_expanded) : null,
        borderRadius: BorderRadius.circular(6),
        child: Container(
          width: double.infinity,
          padding: const EdgeInsets.all(12),
          decoration: BoxDecoration(
            color: AppColors.card,
            border: Border.all(color: AppColors.border),
            borderRadius: BorderRadius.circular(6),
          ),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              header,
              Padding(
                padding: const EdgeInsets.only(top: 6),
                child: Text(c.text ?? '',
                    style: _mono.copyWith(color: AppColors.text)),
              ),
              if (_expanded && hasPath)
                Padding(
                  padding: const EdgeInsets.only(top: 6),
                  child: Text(c.label!, style: _monoDim),
                ),
              if (_expanded && hasBody)
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
    final text = (summary == null || summary!.isEmpty) ? 'Context compressed' : summary!;
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
