import 'package:flutter/material.dart';

import '../models/chunk.dart';
import '../state/tool_detail.dart';
import '../util/model_name.dart';
import 'code_block.dart';
import 'item_detail_screen.dart';
import 'item_row.dart';
import 'subagent_trace_screen.dart';
import 'theme.dart';

const _redColor = Color(0xFFfb4934);
const _mono = TextStyle(fontFamily: 'monospace', fontSize: 11, height: 1.3);
final _monoDim = _mono.copyWith(color: AppColors.dim);

/// Context-pressure color, mirroring the TUI thresholds (50/80). A healthy
/// context stays dim so it doesn't compete with the elevated states.
Color _ctxColor(double pct) {
  if (pct >= 80) return const Color(0xFFfb4934); // red
  if (pct >= 50) return const Color(0xFFfabd2f); // yellow
  return AppColors.dim;
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
      case ChunkKind.compact:
        return _MetaRow(chunk: widget.chunk);
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

  /// Builds the AI header meta line: model · ✻thinking · ▸tools · ctx% · tokens ·
  /// duration. The context % is colored by pressure; everything else stays dim.
  Widget _metaLine(Chunk c) {
    final spans = <InlineSpan>[];
    void add(String text, {Color? color}) {
      if (spans.isNotEmpty) {
        spans.add(TextSpan(text: '  ·  ', style: _monoDim));
      }
      spans.add(TextSpan(
          text: text,
          style: color == null ? _monoDim : _mono.copyWith(color: color)));
    }

    if (c.model?.isNotEmpty ?? false) add(formatModelName(c.model!));
    if (c.thinking > 0) add('✻ ${c.thinking}');
    if (c.toolCount > 0) add('▸ ${c.toolCount}');
    if (c.hasContext) {
      add('${c.contextPct.round()}% ctx', color: _ctxColor(c.contextPct));
    }
    if (c.usage.total > 0) add('${c.usage.total} tok');
    if (c.durationMs > 0) add('${(c.durationMs / 1000).toStringAsFixed(1)}s');

    if (spans.isEmpty) return Text('response', style: _monoDim);
    return Text.rich(TextSpan(children: spans));
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
    if (it.kind == ItemKind.tool) {
      return () => Navigator.of(context).push(
            MaterialPageRoute(
              builder: (_) =>
                  ItemDetailScreen(item: it, detailRef: widget.detailRef),
            ),
          );
    }
    if (it.kind == ItemKind.subagent &&
        (it.hasTrace || (it.agentId?.isNotEmpty ?? false))) {
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

class _MetaRow extends StatelessWidget {
  const _MetaRow({required this.chunk});
  final Chunk chunk;

  @override
  Widget build(BuildContext context) {
    final color = chunk.isError ? _redColor : AppColors.dim;
    final glyph = chunk.kind == ChunkKind.compact ? '⊟' : '·';
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('$glyph ${chunk.summary ?? ''}',
              style: _mono.copyWith(color: color)),
          if (chunk.detail != null && chunk.detail!.isNotEmpty)
            Padding(
              padding: const EdgeInsets.only(left: 14, top: 2),
              child: Text(chunk.detail!,
                  style: _monoDim),
            ),
        ],
      ),
    );
  }
}
