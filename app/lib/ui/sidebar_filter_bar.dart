import 'package:flutter/material.dart';

import 'theme.dart';

/// The left sidebar's bottom bar: a round filter with the current tab's
/// options at its left and a clear button at its right, then Settings.
class SidebarFilterBar extends StatelessWidget {
  const SidebarFilterBar({
    super.key,
    required this.controller,
    required this.hintText,
    required this.onChanged,
    required this.options,
    required this.optionsActive,
    required this.onSettings,
  });

  final TextEditingController controller;
  final String hintText;

  /// Runs on each edit, and with '' when the clear button empties the field
  /// (a controller change alone does not call it).
  final ValueChanged<String> onChanged;

  /// The menu's items, for example [CheckboxMenuButton]s.
  final List<Widget> options;

  /// Whether an option narrows the list; shows a dot on the options button.
  final bool optionsActive;
  final VoidCallback onSettings;

  static const _iconSlot = BoxConstraints.tightFor(width: 36, height: 36);

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Padding(
      // The drawer is not resized for the keyboard, so lift the bar over it.
      padding: EdgeInsets.fromLTRB(
        8,
        6,
        4,
        10 + MediaQuery.viewInsetsOf(context).bottom,
      ),
      child: Row(
        children: [
          Expanded(
            child: SizedBox(
              height: 36,
              child: ValueListenableBuilder<TextEditingValue>(
                valueListenable: controller,
                builder: (context, value, _) => TextField(
                  controller: controller,
                  style: const TextStyle(fontSize: 13),
                  onChanged: onChanged,
                  decoration: InputDecoration(
                    isDense: true,
                    filled: true,
                    fillColor: t.canvas,
                    hintText: hintText,
                    hintStyle: TextStyle(fontSize: 13, color: t.textDim),
                    contentPadding: const EdgeInsets.symmetric(vertical: 10),
                    prefixIcon: _optionsButton(t),
                    // Square icon slots, so each icon is as far from its
                    // side as from the top and bottom.
                    prefixIconConstraints: _iconSlot,
                    suffixIconConstraints: _iconSlot,
                    suffixIcon: value.text.isEmpty
                        ? null
                        : IconButton(
                            tooltip: 'Clear filter',
                            padding: EdgeInsets.zero,
                            constraints: _iconSlot,
                            icon: Icon(
                              Icons.cancel,
                              size: 16,
                              color: t.textDim,
                            ),
                            onPressed: () {
                              controller.clear();
                              onChanged('');
                            },
                          ),
                    border: OutlineInputBorder(
                      borderRadius: BorderRadius.circular(t.radius),
                      borderSide: BorderSide.none,
                    ),
                  ),
                ),
              ),
            ),
          ),
          IconButton(
            tooltip: 'Settings',
            icon: Icon(Icons.settings_outlined, size: 18, color: t.textDim),
            onPressed: onSettings,
          ),
        ],
      ),
    );
  }

  // A MenuAnchor, not a PopupMenuButton: a popup menu opens over its button,
  // and at the screen's bottom edge it would cover it; a menu anchor opens
  // beside its button and flips above it when there is no room below.
  Widget _optionsButton(ArgusTokens t) => MenuAnchor(
    menuChildren: options,
    builder: (context, menu, _) => IconButton(
      tooltip: 'Filter options',
      padding: EdgeInsets.zero,
      constraints: _iconSlot,
      onPressed: () => menu.isOpen ? menu.close() : menu.open(),
      icon: Stack(
        alignment: Alignment.center,
        children: [
          Icon(Icons.tune, size: 16, color: t.textDim),
          if (optionsActive)
            Positioned(
              top: 0,
              right: 0,
              child: Container(
                key: const Key('filter-options-active'),
                width: 6,
                height: 6,
                decoration: BoxDecoration(
                  color: t.link,
                  shape: BoxShape.circle,
                ),
              ),
            ),
        ],
      ),
    ),
  );
}
