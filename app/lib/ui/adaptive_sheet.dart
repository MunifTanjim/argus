import 'package:flutter/material.dart';

import 'responsive.dart';
import 'theme.dart';

/// One entry of an action menu shown by [showActionMenu].
class ActionItem<T> {
  const ActionItem({
    required this.value,
    required this.label,
    this.icon,
    this.subtitle,
    this.danger = false,
    this.enabled = true,
    this.selected = false,
    this.muted = false,
  });

  final T value;
  final String label;
  final Widget? icon;
  final String? subtitle;
  final bool danger;
  final bool enabled;
  final bool selected;

  /// Draws the label in the dim colour, for a note rather than an action.
  final bool muted;
}

/// Shows [items] and returns the chosen value, or null when dismissed. Below
/// the wide desktop threshold this is a bottom sheet; at it, a popup menu
/// anchored at [position], or below [context]'s render box when that is null,
/// so pass the context of the row or button that opened it. [sheetHeader]
/// replaces the title in the bottom sheet.
Future<T?> showActionMenu<T>(
  BuildContext context, {
  String? title,
  required List<ActionItem<T>> items,
  Offset? position,
  Widget? sheetHeader,
}) => useDesktopLayout(context)
    ? _showPopup(context, title, items, position)
    : _showSheet(context, title, sheetHeader, items);

const _titleStyle = TextStyle(fontWeight: FontWeight.w600);

Widget? _icon(ActionItem<Object?> item) => item.icon == null
    ? null
    : IconTheme.merge(
        data: IconThemeData(color: item.danger ? AppColors.error : null),
        child: item.icon!,
      );

TextStyle? _labelStyle(ActionItem<Object?> item) => item.danger
    ? const TextStyle(color: AppColors.error)
    : item.muted
    ? const TextStyle(color: AppColors.dim)
    : null;

Future<T?> _showSheet<T>(
  BuildContext context,
  String? title,
  Widget? header,
  List<ActionItem<T>> items,
) => showModalBottomSheet<T>(
  context: context,
  useRootNavigator: true,
  builder: (ctx) => SafeArea(
    child: SingleChildScrollView(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          if (header != null)
            header
          else if (title != null)
            ListTile(
              title: Text(
                title,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: _titleStyle,
              ),
            ),
          for (final item in items)
            ListTile(
              leading: _icon(item),
              title: Text(item.label, style: _labelStyle(item)),
              subtitle: item.subtitle == null ? null : Text(item.subtitle!),
              selected: item.selected,
              trailing: item.selected ? const Icon(Icons.check) : null,
              enabled: item.enabled,
              onTap: () => Navigator.of(ctx).pop(item.value),
            ),
        ],
      ),
    ),
  ),
);

Future<T?> _showPopup<T>(
  BuildContext context,
  String? title,
  List<ActionItem<T>> items,
  Offset? position,
) {
  final overlay =
      Navigator.of(
            context,
            rootNavigator: true,
          ).overlay!.context.findRenderObject()!
          as RenderBox;
  final Offset anchor;
  if (position != null) {
    anchor = position;
  } else {
    final box = context.findRenderObject() as RenderBox;
    anchor = box.localToGlobal(Offset(0, box.size.height), ancestor: overlay);
  }
  return showMenu<T>(
    context: context,
    useRootNavigator: true,
    position: RelativeRect.fromRect(
      anchor & Size.zero,
      Offset.zero & overlay.size,
    ),
    items: [
      if (title != null)
        PopupMenuItem<T>(
          enabled: false,
          child: Text(
            title,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: _titleStyle,
          ),
        ),
      for (final item in items)
        PopupMenuItem<T>(
          value: item.value,
          enabled: item.enabled,
          child: Row(
            children: [
              if (item.icon != null) ...[
                _icon(item)!,
                const SizedBox(width: 12),
              ],
              Flexible(
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    Text(item.label, style: _labelStyle(item)),
                    if (item.subtitle != null)
                      Text(
                        item.subtitle!,
                        style: const TextStyle(
                          color: AppColors.dim,
                          fontSize: 12,
                        ),
                      ),
                  ],
                ),
              ),
              if (item.selected) ...[
                const SizedBox(width: 12),
                const Icon(Icons.check, size: 18),
              ],
            ],
          ),
        ),
    ],
  );
}
