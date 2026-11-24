import 'package:flutter/foundation.dart' show defaultTargetPlatform, TargetPlatform;
import 'package:flutter/widgets.dart';

/// The native menu bar on macOS; elsewhere returns [child] unchanged.
///
/// The custom items have no key equivalents. AppKit would run a key
/// equivalent before Flutter sees the key, skipping the text field and
/// terminal guards of DesktopShortcuts, which keeps handling the keys. The
/// provided items (Quit, Hide, Minimize) keep their native ones.
class MacMenuBar extends StatefulWidget {
  const MacMenuBar({
    super.key,
    required this.child,
    required this.onBack,
    required this.onSpawn,
    required this.onSettings,
    required this.onQuickSwitcher,
    required this.onShortcutSheet,
    required this.onSelectTab,
  });

  final Widget child;
  final VoidCallback onBack;
  final VoidCallback onSpawn;
  final VoidCallback onSettings;
  final VoidCallback onQuickSwitcher;
  final VoidCallback onShortcutSheet;
  final void Function(int index) onSelectTab;

  @override
  State<MacMenuBar> createState() => _MacMenuBarState();
}

// Flutter has no provided Cut, Copy, Paste or Select All, so these run the
// text editing action of the focused widget.
void _invoke(Intent intent) {
  final ctx = primaryFocus?.context;
  if (ctx != null) Actions.maybeInvoke(ctx, intent);
}

class _MacMenuBarState extends State<MacMenuBar> {
  // Built once and calling through `widget`: PlatformMenuBar sends the menus
  // to the platform again whenever the items are new objects.
  late final List<PlatformMenuItem> _menus = [
    PlatformMenu(label: 'argus', menus: [
      const PlatformMenuItemGroup(members: [
        PlatformProvidedMenuItem(type: PlatformProvidedMenuItemType.about),
      ]),
      PlatformMenuItemGroup(members: [
        PlatformMenuItem(
            label: 'Settings…', onSelected: () => widget.onSettings()),
      ]),
      const PlatformMenuItemGroup(members: [
        PlatformProvidedMenuItem(type: PlatformProvidedMenuItemType.hide),
      ]),
      const PlatformMenuItemGroup(members: [
        PlatformProvidedMenuItem(type: PlatformProvidedMenuItemType.quit),
      ]),
    ]),
    PlatformMenu(label: 'Edit', menus: [
      PlatformMenuItem(
        label: 'Cut',
        onSelected: () => _invoke(
            const CopySelectionTextIntent.cut(SelectionChangedCause.toolbar)),
      ),
      PlatformMenuItem(
        label: 'Copy',
        onSelected: () => _invoke(CopySelectionTextIntent.copy),
      ),
      PlatformMenuItem(
        label: 'Paste',
        onSelected: () =>
            _invoke(const PasteTextIntent(SelectionChangedCause.toolbar)),
      ),
      PlatformMenuItem(
        label: 'Select All',
        onSelected: () =>
            _invoke(const SelectAllTextIntent(SelectionChangedCause.toolbar)),
      ),
    ]),
    PlatformMenu(label: 'View', menus: [
      PlatformMenuItemGroup(members: [
        PlatformMenuItem(
            label: 'Quick Switcher',
            onSelected: () => widget.onQuickSwitcher()),
      ]),
      PlatformMenuItemGroup(members: [
        for (final (i, label) in const ['Sessions', 'History', 'Terminals'].indexed)
          PlatformMenuItem(label: label, onSelected: () => widget.onSelectTab(i)),
      ]),
      PlatformMenuItemGroup(members: [
        PlatformMenuItem(
            label: 'Keyboard Shortcuts',
            onSelected: () => widget.onShortcutSheet()),
      ]),
    ]),
    PlatformMenu(label: 'Session', menus: [
      PlatformMenuItem(label: 'New Session', onSelected: () => widget.onSpawn()),
      PlatformMenuItem(label: 'Back', onSelected: () => widget.onBack()),
    ]),
    const PlatformMenu(label: 'Window', menus: [
      PlatformProvidedMenuItem(type: PlatformProvidedMenuItemType.minimizeWindow),
      PlatformProvidedMenuItem(type: PlatformProvidedMenuItemType.zoomWindow),
    ]),
  ];

  @override
  Widget build(BuildContext context) {
    if (defaultTargetPlatform != TargetPlatform.macOS) return widget.child;
    return PlatformMenuBar(menus: _menus, child: widget.child);
  }
}
