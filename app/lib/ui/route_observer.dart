import 'package:flutter/widgets.dart';

/// App-wide route observer, wired into [MaterialApp.navigatorObservers]. Screens
/// implement [RouteAware] and subscribe to learn when they become visible again
/// after a route on top is popped (e.g. to re-assert push-notification
/// suppression for the session they show).
final RouteObserver<PageRoute<dynamic>> appRouteObserver =
    RouteObserver<PageRoute<dynamic>>();

/// Sits above the home-shell scope navigator and holds its route observer (an
/// observer serves only one navigator).
class ShellNavigatorScope extends InheritedWidget {
  const ShellNavigatorScope({
    super.key,
    required this.observer,
    required super.child,
  });

  final RouteObserver<PageRoute<dynamic>> observer;

  static ShellNavigatorScope? _of(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<ShellNavigatorScope>();

  /// The observer of the navigator holding [context]; [appRouteObserver]
  /// outside the home shell.
  static RouteObserver<PageRoute<dynamic>> observerOf(BuildContext context) =>
      _of(context)?.observer ?? appRouteObserver;

  @override
  bool updateShouldNotify(ShellNavigatorScope oldWidget) =>
      observer != oldWidget.observer;
}
