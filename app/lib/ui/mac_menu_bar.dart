import 'package:flutter/foundation.dart';
import 'package:flutter/scheduler.dart';
import 'package:flutter/widgets.dart';

/// What the app items of the menu bar do; registered by the shell.
class ShellMenuActions {
  const ShellMenuActions({
    required this.onBack,
    required this.onSpawn,
    required this.onSettings,
    required this.onQuickSwitcher,
    required this.onShortcutSheet,
    required this.onSelectTab,
    required this.onToggleSidebar,
    this.sidebarShown,
  });

  final VoidCallback onBack;
  final VoidCallback onSpawn;
  final VoidCallback onSettings;
  final VoidCallback onQuickSwitcher;
  final VoidCallback onShortcutSheet;
  final void Function(int index) onSelectTab;
  final VoidCallback onToggleSidebar;

  /// Whether the sidebar is shown, for the label of View › Hide Sidebar.
  final ValueListenable<bool>? sidebarShown;
}

/// The actions of the mounted shell, if any. The menu bar lives above the
/// navigator for the whole app, while the shell comes and goes.
class ShellMenuRegistry extends ChangeNotifier {
  ShellMenuActions? _actions;
  bool _scheduled = false;
  bool _disposed = false;

  ShellMenuActions? get actions => _actions;

  void register(ShellMenuActions actions) {
    _actions = actions;
    _changed();
  }

  /// Clears [actions] if they are still the registered ones.
  void unregister(ShellMenuActions actions) {
    if (!identical(_actions, actions)) return;
    _actions = null;
    _changed();
  }

  // The shell registers in initState and dispose, mid-frame, and the menu bar
  // is its ancestor, so then the menu bar rebuilds after the frame.
  void _changed() {
    final scheduler = SchedulerBinding.instance;
    if (scheduler.schedulerPhase != SchedulerPhase.persistentCallbacks) {
      notifyListeners();
      return;
    }
    if (_scheduled) return;
    _scheduled = true;
    scheduler.addPostFrameCallback((_) {
      _scheduled = false;
      if (!_disposed) notifyListeners();
    });
  }

  @override
  void dispose() {
    _disposed = true;
    super.dispose();
  }
}

final shellMenuRegistry = ShellMenuRegistry();

/// The native menu bar on macOS; elsewhere returns [child] unchanged. Mount it
/// once for the app: removing a PlatformMenuBar clears the whole menu bar.
///
/// The app items have no key equivalents. Flutter handles a key first, and
/// AppKit redispatches the keys it leaves unhandled to the main menu, so a key
/// equivalent would fire whenever the text field and terminal guards of
/// DesktopShortcuts leave a key alone. DesktopShortcuts keeps handling the
/// keys. The provided items (Quit, Hide, Minimize) keep their native ones.
class MacMenuBar extends StatefulWidget {
  const MacMenuBar({super.key, required this.child, this.registry});

  final Widget child;

  /// Defaults to [shellMenuRegistry].
  final ShellMenuRegistry? registry;

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
  late final ShellMenuRegistry _registry =
      widget.registry ?? shellMenuRegistry;
  // PlatformMenuBar sends the menus to the platform again whenever the items
  // are new objects, so they are rebuilt only when the app items turn on or
  // off or the sidebar item's label changes, and call the registered actions
  // when selected.
  late bool _builtEnabled;
  late bool _builtSidebarShown;
  late List<PlatformMenuItem> _menus;
  ValueListenable<bool>? _sidebar;

  @override
  void initState() {
    super.initState();
    _listenSidebar();
    _menus = _build();
    _registry.addListener(_onRegistry);
  }

  @override
  void dispose() {
    _registry.removeListener(_onRegistry);
    _sidebar?.removeListener(_update);
    super.dispose();
  }

  bool get _enabled => _registry.actions != null;

  bool get _sidebarShown => _sidebar?.value ?? true;

  void _listenSidebar() {
    final sidebar = _registry.actions?.sidebarShown;
    if (sidebar == _sidebar) return;
    _sidebar?.removeListener(_update);
    _sidebar = sidebar?..addListener(_update);
  }

  void _onRegistry() {
    _listenSidebar();
    _update();
  }

  void _update() {
    if (_enabled == _builtEnabled && _sidebarShown == _builtSidebarShown) {
      return;
    }
    setState(() => _menus = _build());
  }

  // Null, so disabled, while no shell is registered.
  VoidCallback? _app(void Function(ShellMenuActions a) call) => _builtEnabled
      ? () {
          if (_registry.actions case final a?) call(a);
        }
      : null;

  List<PlatformMenuItem> _build() {
    _builtEnabled = _enabled;
    _builtSidebarShown = _sidebarShown;
    return [
      PlatformMenu(label: 'Argus', menus: [
        const PlatformMenuItemGroup(members: [
          PlatformProvidedMenuItem(type: PlatformProvidedMenuItemType.about),
        ]),
        PlatformMenuItemGroup(members: [
          PlatformMenuItem(
              label: 'Settings…', onSelected: _app((a) => a.onSettings())),
        ]),
        const PlatformMenuItemGroup(members: [
          PlatformProvidedMenuItem(type: PlatformProvidedMenuItemType.hide),
          PlatformProvidedMenuItem(
              type: PlatformProvidedMenuItemType.hideOtherApplications),
          PlatformProvidedMenuItem(
              type: PlatformProvidedMenuItemType.showAllApplications),
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
              label: _builtSidebarShown ? 'Hide Sidebar' : 'Show Sidebar',
              onSelected: _app((a) => a.onToggleSidebar())),
        ]),
        PlatformMenuItemGroup(members: [
          PlatformMenuItem(
              label: 'Quick Switcher',
              onSelected: _app((a) => a.onQuickSwitcher())),
        ]),
        PlatformMenuItemGroup(members: [
          for (final (i, label)
              in const ['Sessions', 'History', 'Terminals'].indexed)
            PlatformMenuItem(
                label: label, onSelected: _app((a) => a.onSelectTab(i))),
        ]),
        PlatformMenuItemGroup(members: [
          PlatformMenuItem(
              label: 'Keyboard Shortcuts',
              onSelected: _app((a) => a.onShortcutSheet())),
        ]),
      ]),
      PlatformMenu(label: 'Session', menus: [
        PlatformMenuItem(
            label: 'New Session', onSelected: _app((a) => a.onSpawn())),
        PlatformMenuItem(label: 'Back', onSelected: _app((a) => a.onBack())),
      ]),
      const PlatformMenu(label: 'Window', menus: [
        PlatformProvidedMenuItem(
            type: PlatformProvidedMenuItemType.minimizeWindow),
        PlatformProvidedMenuItem(type: PlatformProvidedMenuItemType.zoomWindow),
        PlatformProvidedMenuItem(
            type: PlatformProvidedMenuItemType.toggleFullScreen),
      ]),
    ];
  }

  @override
  Widget build(BuildContext context) {
    if (defaultTargetPlatform != TargetPlatform.macOS) return widget.child;
    return PlatformMenuBar(menus: _menus, child: widget.child);
  }
}
