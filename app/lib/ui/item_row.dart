import 'package:flutter/material.dart';

import '../models/entry.dart';
import 'theme.dart';
import 'tool_registry.dart';


class ItemRow extends StatelessWidget {
  const ItemRow({super.key, required this.item, this.agent, this.onTap});

  final Entry item;

  /// The transcript's agent, for tool lookups.
  final String? agent;

  /// When set, the row is tappable (drill into a full-screen detail) and shows a
  /// trailing chevron.
  final VoidCallback? onTap;

  static const _mono =
      TextStyle(fontFamily: monoFamily, fontSize: 12, height: 1.3);

  @override
  Widget build(BuildContext context) {
    switch (item.kind) {
      case EntryKind.tool:
        final err = item.resultIsError;
        final meta = toolMeta(agent, item.toolName);
        final name = (meta?.display.isNotEmpty ?? false)
            ? meta!.display
            : (item.toolName ?? 'tool');
        return _row(
          context,
          leading: Icon(
              meta != null ? categoryIcon(meta.category) : Icons.play_arrow,
              size: 14,
              color: err
                  ? context.tokens.error
                  : (meta != null
                      ? categoryColor(meta.category, context.tokens)
                      : context.tokens.accent)),
          label: name,
          labelColor: err ? context.tokens.error : context.tokens.accent,
          // Error is color-only visually; announce it for screen readers.
          labelSemantics: err ? '$name, error' : null,
          preview: item.inputPreview,
        );
      case EntryKind.skill:
        return _row(
          context,
          leading: Icon(categoryIcon(ToolCategory.skill),
              size: 14, color: categoryColor(ToolCategory.skill, context.tokens)),
          label: 'Skill',
          labelColor: context.tokens.accent,
          preview: item.inputPreview,
        );
      case EntryKind.subagent:
        final tm = item.soleSubagent;
        if (tm?.isTeammate ?? false) {
          final tc = readableOn(
            teamBrandColor(tm!.color, context.tokens),
            context.tokens,
          );
          return _row(
          context,
            leading: Icon(Icons.forum_outlined, size: 14, color: tc),
            label: tm.name.isNotEmpty ? tm.name : 'teammate',
            labelColor: tc,
            preview: tm.idle ? 'is done' : item.text,
          );
        }
        if (isAgentRefTool(item.toolName)) {
          return _row(
          context,
            leading: Icon(Icons.smart_toy_outlined,
                size: 14, color: context.tokens.accent),
            label: toolMeta('codex', item.toolName)!.display,
            labelColor: context.tokens.accent,
            preview: item.subagents
                .map((s) => s.name.isNotEmpty ? s.name : s.id)
                .where((n) => n.isNotEmpty)
                .join(', '),
          );
        }
        final sub = item.soleSubagent;
        final type = sub?.type ?? '';
        final name = sub?.name ?? '';
        final label = name.isNotEmpty
            ? (type.isNotEmpty ? '$name ($type)' : name)
            : (type.isNotEmpty ? type : 'subagent');
        return _row(
          context,
          leading: Icon(Icons.smart_toy_outlined,
              size: 14, color: context.tokens.accent),
          label: label,
          labelColor: context.tokens.accent,
          preview: sub?.desc,
        );
      default:
        return const SizedBox.shrink();
    }
  }

  Widget _row(
    BuildContext context, {
    required Widget leading,
    required String label,
    required Color labelColor,
    String? labelSemantics,
    String? preview,
  }) {
    final monoDim = _mono.copyWith(color: context.tokens.textDim);
    final hasPreview = preview != null && preview.trim().isNotEmpty;
    final labelText = Text(label,
        semanticsLabel: labelSemantics,
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
        style: _mono.copyWith(color: labelColor, fontWeight: FontWeight.w600));
    final row = Padding(
      padding: const EdgeInsets.symmetric(vertical: 3),
      child: LayoutBuilder(
        builder: (context, constraints) => Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.only(right: 6, top: 1),
              child: leading,
            ),
            // Cap the label so a long one leaves room for the preview.
            if (hasPreview)
              ConstrainedBox(
                constraints:
                    BoxConstraints(maxWidth: constraints.maxWidth * 0.6),
                child: labelText,
              )
            else
              Flexible(child: labelText),
            if (hasPreview) ...[
              const SizedBox(width: 8),
              Expanded(
                child: Text(preview.trim(),
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: monoDim),
              ),
            ],
            if (onTap != null) Text(' ›', style: monoDim),
          ],
        ),
      ),
    );
    if (onTap == null) return row;
    return InkWell(onTap: onTap, child: row);
  }
}

