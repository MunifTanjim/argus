import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

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
    this.compact = false,
  });

  final String title;
  final String? label;
  final Widget? mark;
  final String? node;
  final List<(String, Color)> meta;

  /// The in-feed form: each line is cut to one line and a rule ends the
  /// header. The details sheet form, the default, wraps each line and has no
  /// rule.
  final bool compact;

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
                maxLines: compact ? 1 : null,
                overflow: compact ? TextOverflow.ellipsis : null,
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
            maxLines: compact ? 1 : null,
            overflow: compact ? TextOverflow.ellipsis : null,
            style: big,
          ),
        if (meta.isNotEmpty) ...[
          const SizedBox(height: 2),
          Text.rich(
            TextSpan(style: small, children: [metaSpan(meta, t)]),
            maxLines: compact ? 1 : null,
            overflow: compact ? TextOverflow.ellipsis : null,
          ),
        ],
        if (compact) ...[
          const SizedBox(height: 12),
          const Divider(height: 1),
        ],
      ],
    );
  }
}

/// Opens a details bottom sheet with the shared drag handle and padding.
/// The [body] children stack in a column that scrolls when it is taller than
/// the sheet; the [footer], when given, stays in view under it. Both rebuild
/// with the providers they watch, so the sheet can follow live state. Their
/// context is inside the sheet: [Navigator.pop] on it closes the sheet.
Future<void> showDetailsSheet(
  BuildContext context, {
  required List<Widget> Function(BuildContext context, WidgetRef ref) body,
  Widget Function(BuildContext context, WidgetRef ref)? footer,
}) {
  return showModalBottomSheet<void>(
    context: context,
    useRootNavigator: true,
    showDragHandle: true,
    builder: (sheet) => SafeArea(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Flexible(
            child: SingleChildScrollView(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
              child: Consumer(
                builder: (context, ref, _) => Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisSize: MainAxisSize.min,
                  children: body(context, ref),
                ),
              ),
            ),
          ),
          if (footer != null)
            Consumer(builder: (context, ref, _) => footer(context, ref)),
        ],
      ),
    ),
  );
}

/// Copies a value and marks the row "Copied" for a moment. A snackbar would
/// show under the sheet.
mixin _CopyNotice<T extends StatefulWidget> on State<T> {
  Timer? _reset;

  bool get copied => _reset != null;

  @override
  void dispose() {
    _reset?.cancel();
    super.dispose();
  }

  void copy(String text) {
    Clipboard.setData(ClipboardData(text: text));
    _reset?.cancel();
    _reset = Timer(const Duration(seconds: 1), () {
      if (mounted) setState(() => _reset = null);
    });
    setState(() {});
  }
}

/// A small labeled field of a details sheet. A tap copies the [value] and
/// keeps the sheet open.
class DetailsFieldRow extends StatefulWidget {
  const DetailsFieldRow({
    super.key,
    required this.label,
    required this.value,
    this.color,
    this.copyable = true,
    this.copyValue,
  });

  final String label;
  final String value;

  /// What a tap copies when it differs from the shown [value], for example
  /// the full commit id behind a short one.
  final String? copyValue;

  /// The value's color, for example the error color; null is the default.
  final Color? color;

  /// Whether a tap copies the value; summaries such as counts do not.
  final bool copyable;

  @override
  State<DetailsFieldRow> createState() => _DetailsFieldRowState();
}

class _DetailsFieldRowState extends State<DetailsFieldRow> with _CopyNotice {
  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final style = mono.copyWith(fontSize: 12);
    return InkWell(
      borderRadius: BorderRadius.circular(t.radius),
      onTap: widget.copyable
          ? () => copy(widget.copyValue ?? widget.value)
          : null,
      child: Padding(
        padding: const EdgeInsets.symmetric(vertical: 3),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            SizedBox(
              width: 96,
              child: Text(
                widget.label,
                style: style.copyWith(color: t.textDim),
              ),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                copied ? 'Copied' : widget.value,
                style: style.copyWith(
                  color: copied ? t.success : (widget.color ?? t.textSecondary),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// One action of a details sheet: what its tiles and its pinned bar show and
/// run.
class SheetAction {
  const SheetAction({
    required this.label,
    required this.icon,
    required this.run,
    this.destructive = false,
  });

  final String label;
  final Widget icon;
  final bool destructive;

  /// Runs after the sheet closes.
  final VoidCallback run;
}

/// [actions] as list tiles that fit a details sheet's padded body, under its
/// table; a tap closes the [sheet] and runs the action.
List<Widget> sheetActionTiles(BuildContext sheet, List<SheetAction> actions) {
  final t = sheet.tokens;
  return [
    for (final a in actions)
      ListTile(
        contentPadding: EdgeInsets.zero,
        dense: true,
        leading: IconTheme.merge(
          data: IconThemeData(color: a.destructive ? t.destructive : null),
          child: a.icon,
        ),
        title: Text(
          a.label,
          style: a.destructive ? TextStyle(color: t.destructive) : null,
        ),
        onTap: () {
          Navigator.of(sheet).pop();
          a.run();
        },
      ),
  ];
}

/// The pinned footer of a details sheet: [actions] as buttons side by side,
/// the destructive ones at the left, or the [note] when there are none.
class DetailsActionBar extends StatelessWidget {
  const DetailsActionBar({
    super.key,
    required this.sheet,
    required this.actions,
    this.note,
  });

  /// The sheet's own context, closed before an action runs.
  final BuildContext sheet;
  final List<SheetAction> actions;
  final String? note;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final note = this.note;
    final ordered = [
      ...actions.where((a) => a.destructive),
      ...actions.where((a) => !a.destructive),
    ];
    if (note == null && ordered.isEmpty) return const SizedBox.shrink();
    return DecoratedBox(
      decoration: BoxDecoration(
        border: Border(top: BorderSide(color: t.line)),
      ),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 8, 16, 12),
        child: note != null
            ? Text(note, style: TextStyle(color: t.textDim))
            : Row(
                children: [
                  for (final (i, a) in ordered.indexed) ...[
                    if (i > 0) const SizedBox(width: 8),
                    Expanded(
                      child: OutlinedButton.icon(
                        style: OutlinedButton.styleFrom(
                          foregroundColor: a.destructive ? t.destructive : t.text,
                        ),
                        icon: IconTheme.merge(
                          data: const IconThemeData(size: 18),
                          child: a.icon,
                        ),
                        label: Text(a.label),
                        onPressed: () {
                          Navigator.of(sheet).pop();
                          a.run();
                        },
                      ),
                    ),
                  ],
                ],
              ),
      ),
    );
  }
}
