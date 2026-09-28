import 'package:flutter/material.dart';

/// Tells screens inside the home shell how to open the modal projects drawer.
/// [openDrawer] is null when the drawer is a fixed side panel.
class ShellDrawerScope extends InheritedWidget {
  const ShellDrawerScope({super.key, this.openDrawer, required super.child});

  final VoidCallback? openDrawer;

  static VoidCallback? openDrawerOf(BuildContext context) => context
      .dependOnInheritedWidgetOfExactType<ShellDrawerScope>()
      ?.openDrawer;

  @override
  bool updateShouldNotify(ShellDrawerScope oldWidget) =>
      (openDrawer == null) != (oldWidget.openDrawer == null);
}

/// The button that opens the modal projects drawer: the AppBar leading of a
/// scope root, the first AppBar action of a screen pushed above one. Null when
/// there is no modal drawer to open.
Widget? shellMenuButton(BuildContext context) {
  final open = ShellDrawerScope.openDrawerOf(context);
  if (open == null) return null;
  return IconButton(
    icon: const Icon(Icons.menu),
    tooltip: 'Projects',
    onPressed: open,
  );
}
