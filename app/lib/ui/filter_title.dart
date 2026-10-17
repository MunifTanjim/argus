import 'package:flutter/material.dart';

/// An app bar title that a filter field replaces while [query] is not null.
class FilterTitle extends StatelessWidget {
  const FilterTitle({
    super.key,
    required this.child,
    required this.query,
    required this.hint,
    required this.onChanged,
    required this.onClose,
  });

  final Widget child;
  final String? query;
  final String hint;
  final ValueChanged<String> onChanged;
  final VoidCallback onClose;

  @override
  Widget build(BuildContext context) {
    if (query == null) return child;
    return TextField(
      autofocus: true,
      decoration: InputDecoration(
        hintText: hint,
        border: InputBorder.none,
        suffixIcon: IconButton(
          icon: const Icon(Icons.close),
          tooltip: 'Clear filter',
          onPressed: onClose,
        ),
      ),
      onChanged: (v) => onChanged(v.trim()),
    );
  }
}

/// The app bar action that opens a [FilterTitle]; hidden while it is open.
class FilterButton extends StatelessWidget {
  const FilterButton({
    super.key,
    required this.query,
    required this.tooltip,
    required this.onOpen,
  });

  final String? query;
  final String tooltip;
  final VoidCallback onOpen;

  @override
  Widget build(BuildContext context) {
    if (query != null) return const SizedBox.shrink();
    return IconButton(
      icon: const Icon(Icons.search),
      tooltip: tooltip,
      onPressed: onOpen,
    );
  }
}
