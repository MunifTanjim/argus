import 'package:flutter/material.dart';

import '../models/chunk.dart';
import 'theme.dart';
import 'tool_registry.dart';

const _redColor = Color(0xFFfb4934);

class ItemRow extends StatelessWidget {
  const ItemRow({super.key, required this.item, this.onTap});

  final Item item;

  /// When set, the row is tappable (drill into a full-screen detail) and shows a
  /// trailing chevron.
  final VoidCallback? onTap;

  static const _mono =
      TextStyle(fontFamily: 'monospace', fontSize: 12, height: 1.3);
  static final _monoDim = _mono.copyWith(color: AppColors.dim);

  @override
  Widget build(BuildContext context) {
    switch (item.kind) {
      case ItemKind.text:
      case ItemKind.unknown:
        return const SizedBox.shrink();
      case ItemKind.thinking:
        return _row(
          leading: const Icon(Icons.lightbulb, size: 14, color: AppColors.dim),
          label: 'Thinking',
          labelColor: AppColors.dim,
          trailing: item.signature ? ' 🔒' : '',
          preview: item.text,
        );
      case ItemKind.tool:
        final err = item.resultIsError;
        final meta = toolMeta(item.toolName);
        final name = (meta?.display.isNotEmpty ?? false)
            ? meta!.display
            : (item.toolName ?? 'tool');
        return _row(
          leading: Icon(
              meta != null ? categoryIcon(meta.category) : Icons.play_arrow,
              size: 14,
              color: err
                  ? _redColor
                  : (meta != null
                      ? categoryColor(meta.category)
                      : AppColors.accent)),
          label: name,
          labelColor: err ? _redColor : AppColors.accent,
          // Error is color-only visually; announce it for screen readers.
          labelSemantics: err ? '$name, error' : null,
          preview: item.inputPreview,
        );
      case ItemKind.subagent:
        // wait/close reference existing agents; label by op with target names.
        if (item.toolName == 'wait_agent' || item.toolName == 'close_agent') {
          return _row(
            leading: const Icon(Icons.smart_toy_outlined,
                size: 14, color: AppColors.accent),
            label: item.toolName == 'wait_agent' ? 'Wait Agent' : 'Close Agent',
            labelColor: AppColors.accent,
            preview: item.subagents
                .map((s) => s.name.isNotEmpty ? s.name : s.id)
                .where((n) => n.isNotEmpty)
                .join(', '),
          );
        }
        return _row(
          leading: const Icon(Icons.smart_toy_outlined,
              size: 14, color: AppColors.accent),
          label: item.soleSubagent?.type ?? 'subagent',
          labelColor: AppColors.accent,
          preview: item.soleSubagent?.desc,
        );
    }
  }

  Widget _row({
    required Widget leading,
    required String label,
    required Color labelColor,
    String trailing = '',
    String? labelSemantics,
    String? preview,
  }) {
    final row = Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Padding(
            padding: const EdgeInsets.only(right: 6, top: 1),
            child: leading,
          ),
          Text('$label$trailing',
              semanticsLabel: labelSemantics,
              style: _mono.copyWith(
                  color: labelColor, fontWeight: FontWeight.w600)),
          if (preview != null && preview.trim().isNotEmpty) ...[
            const SizedBox(width: 8),
            Expanded(
              child: Text(preview.trim(),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: _monoDim),
            ),
          ],
          if (onTap != null) Text(' ›', style: _monoDim),
        ],
      ),
    );
    if (onTap == null) return row;
    return InkWell(onTap: onTap, child: row);
  }
}

