import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';

import 'responsive.dart';
import 'titlebar.dart';

class AdaptiveNavDestination {
  const AdaptiveNavDestination({required this.icon, required this.label});

  final Widget icon;
  final String label;
}

/// A [Scaffold] with a bottom [NavigationBar], or a side [NavigationRail] on
/// wide desktop windows.
class AdaptiveNavScaffold extends StatefulWidget {
  const AdaptiveNavScaffold({
    super.key,
    required this.body,
    required this.destinations,
    required this.selectedIndex,
    required this.onSelected,
    this.floatingActionButton,
    this.appBar,
  });

  final Widget body;
  final List<AdaptiveNavDestination> destinations;
  final int selectedIndex;
  final ValueChanged<int> onSelected;
  final Widget? floatingActionButton;
  final PreferredSizeWidget? appBar;

  @override
  State<AdaptiveNavScaffold> createState() => _AdaptiveNavScaffoldState();
}

final List<_AdaptiveNavScaffoldState> _mounted = [];

/// Selects tab [index] of the topmost visible [AdaptiveNavScaffold]. Returns
/// false if none is visible or [index] is out of range.
bool selectVisibleTab(int index) {
  for (final state in _mounted.reversed) {
    if (!(ModalRoute.of(state.context)?.isCurrent ?? true)) continue;
    if (index < 0 || index >= state.widget.destinations.length) return false;
    state.widget.onSelected(index);
    return true;
  }
  return false;
}

class _AdaptiveNavScaffoldState extends State<AdaptiveNavScaffold> {
  @override
  void initState() {
    super.initState();
    _mounted.add(this);
  }

  @override
  void dispose() {
    _mounted.remove(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final wide = useDesktopLayout(context);
    final rail = NavigationRail(
      labelType: NavigationRailLabelType.all,
      selectedIndex: widget.selectedIndex,
      onDestinationSelected: widget.onSelected,
      destinations: [
        for (final d in widget.destinations)
          NavigationRailDestination(icon: d.icon, label: Text(d.label)),
      ],
    );
    // Without an app bar above it, the rail would reach into the macOS
    // toolbar row, where clicks drag the window.
    final underToolbar =
        widget.appBar == null && defaultTargetPlatform == TargetPlatform.macOS;
    return Scaffold(
      appBar: widget.appBar,
      floatingActionButton: widget.floatingActionButton,
      body: wide
          ? Row(
              children: [
                underToolbar
                    ? Padding(
                        padding: const EdgeInsets.only(top: kMacToolbarHeight),
                        child: rail,
                      )
                    : rail,
                const VerticalDivider(width: 1),
                Expanded(child: widget.body),
              ],
            )
          : widget.body,
      bottomNavigationBar: wide
          ? null
          : NavigationBar(
              selectedIndex: widget.selectedIndex,
              onDestinationSelected: widget.onSelected,
              destinations: [
                for (final d in widget.destinations)
                  NavigationDestination(icon: d.icon, label: d.label),
              ],
            ),
    );
  }
}
