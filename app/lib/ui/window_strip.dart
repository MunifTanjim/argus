import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';

import 'theme.dart';
import 'titlebar.dart';

const double kMacTitleStripHeight = 28;
const Color kWindowStripColor = AppColors.canvas;
// A light tint over the macOS sidebar vibrancy, so text contrast stays.
const Color kSidebarTint = Color(0x14000000);

/// Tracks the root navigator's routes so the strip can match the translucent
/// sidebar of the route that shows one, until an opaque route covers it.
class SidebarStripController extends NavigatorObserver with ChangeNotifier {
  final _routes = <Route<dynamic>>[];
  Route<dynamic>? _owner;
  double _width = 0;
  bool _scheduled = false;
  bool _disposed = false;

  /// The width at the left of the strip that is tinted like the sidebar.
  double get width {
    final i = _owner == null ? -1 : _routes.indexOf(_owner!);
    if (i < 0) return 0;
    final covered =
        _routes.skip(i + 1).any((r) => r is TransitionRoute && r.opaque);
    return covered ? 0 : _width;
  }

  /// [owner] shows a sidebar [width] wide at the left of the window.
  void show(Route<dynamic> owner, double width) {
    if (_owner == owner && _width == width) return;
    _owner = owner;
    _width = width;
    _changed();
  }

  void hide(Route<dynamic> owner) {
    if (_owner != owner) return;
    _owner = null;
    _width = 0;
    _changed();
  }

  // Calls can arrive mid-build (initial routes, the owner's build), and the
  // strip is an ancestor of the navigator, so then it rebuilds after the frame.
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

  @override
  void didPush(Route<dynamic> route, Route<dynamic>? previousRoute) {
    _routes.add(route);
    _changed();
  }

  @override
  void didPop(Route<dynamic> route, Route<dynamic>? previousRoute) {
    _routes.remove(route);
    _changed();
  }

  @override
  void didRemove(Route<dynamic> route, Route<dynamic>? previousRoute) {
    _routes.remove(route);
    _changed();
  }

  @override
  void didReplace({Route<dynamic>? newRoute, Route<dynamic>? oldRoute}) {
    final i = oldRoute == null ? -1 : _routes.indexOf(oldRoute);
    if (i < 0 || newRoute == null) return;
    _routes[i] = newRoute;
    _changed();
  }
}

/// On macOS the title bar is transparent and the content extends under it, so
/// a strip above the navigator holds the traffic lights. The strip lies in
/// the native toolbar area, which drags the window. In full screen there are
/// no traffic lights (the root [TitlebarInsets] is 0), and the strip
/// collapses.
class WindowStrip extends StatelessWidget {
  const WindowStrip({super.key, this.controller, required this.child});

  /// Also a navigator observer of the root navigator.
  final SidebarStripController? controller;
  final Widget child;

  /// The controller a route with a translucent sidebar reports its width to;
  /// null where there is no strip.
  static SidebarStripController? sidebarOf(BuildContext context) => context
      .dependOnInheritedWidgetOfExactType<_SidebarStripScope>()
      ?.controller;

  @override
  Widget build(BuildContext context) {
    if (defaultTargetPlatform != TargetPlatform.macOS) return child;
    final fullScreen = TitlebarInsets.of(context) == 0;
    final controller = this.controller;
    return Column(
      children: [
        SizedBox(
          key: const ValueKey('window-strip'),
          height: fullScreen ? 0 : kMacTitleStripHeight,
          width: double.infinity,
          child: controller == null
              ? const ColoredBox(color: kWindowStripColor)
              : ListenableBuilder(
                  listenable: controller,
                  builder: (context, _) => _background(controller.width),
                ),
        ),
        Expanded(
          child: controller == null
              ? child
              : _SidebarStripScope(controller: controller, child: child),
        ),
      ],
    );
  }

  Widget _background(double sidebarWidth) {
    if (sidebarWidth <= 0) return const ColoredBox(color: kWindowStripColor);
    return Row(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        SizedBox(
          width: sidebarWidth,
          child: const ColoredBox(
            key: ValueKey('window-strip-sidebar'),
            color: kSidebarTint,
          ),
        ),
        const Expanded(child: ColoredBox(color: kWindowStripColor)),
      ],
    );
  }
}

class _SidebarStripScope extends InheritedWidget {
  const _SidebarStripScope({required this.controller, required super.child});

  final SidebarStripController controller;

  @override
  bool updateShouldNotify(_SidebarStripScope oldWidget) =>
      controller != oldWidget.controller;
}
