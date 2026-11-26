import 'package:flutter/material.dart';

import 'code_block.dart';
import 'node_header.dart';
import 'theme.dart';
import 'usage_format.dart';

/// The details header shared by the live session view and the history
/// details sheets: an optional status [mark], then [label] (small) with the
/// [node] at the right, then [title] (large), then the colored [meta] line.
/// With no [label], the first line is the title itself.
class DetailsHeader extends StatelessWidget {
  const DetailsHeader({
    super.key,
    required this.title,
    this.label,
    this.mark,
    this.node,
    this.meta = const [],
    this.wrap = false,
  });

  final String title;
  final String? label;
  final Widget? mark;
  final String? node;
  final List<(String, Color)> meta;

  /// The details sheet form: the label, title, and meta line wrap, not cut,
  /// and no rule shows under the header. The compact in-feed header cuts
  /// each line and ends with a rule.
  final bool wrap;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final small = mono.copyWith(color: t.textDim, fontSize: 11);
    final big = mono.copyWith(
      color: t.text,
      fontSize: 17,
      fontWeight: FontWeight.w600,
    );
    final label = this.label;
    final node = this.node;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Row(
          children: [
            if (mark != null) ...[mark!, const SizedBox(width: 8)],
            Expanded(
              child: Text(
                label ?? title,
                maxLines: wrap ? null : 1,
                overflow: wrap ? null : TextOverflow.ellipsis,
                style: label == null ? big : small,
              ),
            ),
            if (node != null && node.isNotEmpty) ...[
              const SizedBox(width: 8),
              // Gives way to the title, but keeps a short name whole and at
              // the right edge.
              Flexible(
                child: Align(
                  alignment: Alignment.centerRight,
                  child: NodeName(node),
                ),
              ),
            ],
          ],
        ),
        if (label != null)
          Text(
            title,
            maxLines: wrap ? null : 1,
            overflow: wrap ? null : TextOverflow.ellipsis,
            style: big,
          ),
        if (meta.isNotEmpty) ...[
          const SizedBox(height: 2),
          Text.rich(
            TextSpan(style: small, children: [metaSpan(meta, t)]),
            maxLines: wrap ? null : 1,
            overflow: wrap ? null : TextOverflow.ellipsis,
          ),
        ],
        if (!wrap) ...[
          const SizedBox(height: 12),
          Divider(height: 1, color: t.line),
        ],
      ],
    );
  }
}

/// Opens a details bottom sheet with the shared drag handle and padding.
Future<void> showDetailsSheet(BuildContext context, WidgetBuilder builder) {
  return showModalBottomSheet<void>(
    context: context,
    useRootNavigator: true,
    showDragHandle: true,
    builder: (sheet) => SafeArea(
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
        child: builder(sheet),
      ),
    ),
  );
}

/// A details sheet's full path; a tap closes the [sheet] and copies the path.
class DetailsPathRow extends StatelessWidget {
  const DetailsPathRow({super.key, required this.path, required this.sheet});

  final String path;

  /// The sheet's own context, closed before the copy.
  final BuildContext sheet;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return InkWell(
      borderRadius: BorderRadius.circular(t.radius),
      // Copies while the sheet is still mounted; the notice shows on the
      // screen under it.
      onTap: () {
        copyToClipboard(sheet, path);
        Navigator.of(sheet).pop();
      },
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 6),
        child: Row(
          children: [
            Expanded(
              child: Text(path, style: mono.copyWith(color: t.textSecondary)),
            ),
            const SizedBox(width: 8),
            Icon(Icons.copy, size: 16, color: t.textDim),
          ],
        ),
      ),
    );
  }
}
