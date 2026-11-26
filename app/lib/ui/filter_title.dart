import 'package:flutter/material.dart';

import 'theme.dart';

/// An app bar title that a filter field replaces while [query] is not null.
class FilterTitle extends StatelessWidget {
  const FilterTitle({
    super.key,
    required this.child,
    required this.query,
    required this.hint,
    required this.onChanged,
  });

  final Widget child;
  final String? query;
  final String hint;
  final ValueChanged<String> onChanged;

  @override
  Widget build(BuildContext context) {
    if (query == null) return child;
    return FilterField(hint: hint, onChanged: (v) => onChanged(v.trim()));
  }
}

/// The filter field of an app bar title: in the title's font, and centered in
/// the bar as the title is.
class FilterField extends StatelessWidget {
  const FilterField({
    super.key,
    required this.hint,
    required this.onChanged,
    this.controller,
  });

  final String hint;
  final ValueChanged<String> onChanged;
  final TextEditingController? controller;

  @override
  Widget build(BuildContext context) {
    final style = Theme.of(context).appBarTheme.titleTextStyle;
    return TextField(
      controller: controller,
      autofocus: true,
      style: style,
      decoration: InputDecoration(
        hintText: hint,
        hintStyle: style?.copyWith(color: context.tokens.textDim),
        border: InputBorder.none,
        isCollapsed: true,
      ),
      onChanged: onChanged,
    );
  }
}

/// The app bar action that opens a [FilterTitle]. While it is open, the
/// action closes it, in the same place.
class FilterButton extends StatelessWidget {
  const FilterButton({
    super.key,
    required this.query,
    required this.tooltip,
    required this.onOpen,
    required this.onClose,
  });

  final String? query;
  final String tooltip;
  final VoidCallback onOpen;
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) => query == null
      ? IconButton(
          icon: const Icon(Icons.search),
          tooltip: tooltip,
          onPressed: onOpen,
        )
      : IconButton(
          icon: const Icon(Icons.close),
          tooltip: 'Clear filter',
          onPressed: onClose,
        );
}
