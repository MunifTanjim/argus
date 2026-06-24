import 'package:flutter/material.dart';

import '../models/chunk.dart';
import 'theme.dart';

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
          leading: '✻',
          label: 'thinking',
          labelColor: AppColors.dim,
          trailing: item.signature ? ' 🔒' : '',
          preview: item.text,
        );
      case ItemKind.tool:
        return _row(
          leading: '▸',
          label: item.toolName ?? 'tool',
          labelColor: item.resultIsError ? _redColor : AppColors.accent,
          preview: item.inputPreview,
        );
      case ItemKind.subagent:
        return _row(
          leading: '▸',
          label: item.subagentType ?? 'subagent',
          labelColor: AppColors.accent,
          preview: item.subagentDesc,
        );
    }
  }

  Widget _row({
    required String leading,
    required String label,
    required Color labelColor,
    String trailing = '',
    String? preview,
  }) {
    final row = Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text('$leading ', style: _monoDim),
          Text('$label$trailing',
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
          if (onTap != null)
            Text(' ›', style: _monoDim),
        ],
      ),
    );
    if (onTap == null) return row;
    return InkWell(onTap: onTap, child: row);
  }
}
