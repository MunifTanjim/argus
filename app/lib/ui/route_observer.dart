import 'package:flutter/widgets.dart';

/// App-wide route observer, wired into [MaterialApp.navigatorObservers]. Screens
/// implement [RouteAware] and subscribe to learn when they become visible again
/// after a route on top is popped (e.g. to re-assert push-notification
/// suppression for the session they show).
final RouteObserver<PageRoute<dynamic>> appRouteObserver =
    RouteObserver<PageRoute<dynamic>>();
