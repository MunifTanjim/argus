import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/scheduler.dart';
import 'package:macos_window_utils/macos/ns_window_delegate.dart';
import 'package:macos_window_utils/macos_window_utils.dart';
import 'package:macos_window_utils/widgets/macos_toolbar_passthrough.dart';

/// The height of the unified toolbar row on macOS; the traffic lights are
/// centered in it.
const double kMacToolbarHeight = 52;

/// The space at the left of the toolbar row that the traffic lights take.
const double kTrafficLightsInset = 80;

bool get _isMac => defaultTargetPlatform == TargetPlatform.macOS;

/// The space a toolbar at this place must leave at its left for the traffic
/// lights.
class TitlebarInsets extends InheritedWidget {
  const TitlebarInsets({super.key, required this.left, required super.child});

  final double left;

  /// 0 where no [TitlebarInsets] is above [context].
  static double of(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<TitlebarInsets>()?.left ?? 0;

  @override
  bool updateShouldNotify(TitlebarInsets oldWidget) => left != oldWidget.left;
}

/// Provides the root [TitlebarInsets]: [kTrafficLightsInset] on macOS while
/// not in full screen (where there are no traffic lights), else 0.
class TitlebarScope extends StatefulWidget {
  const TitlebarScope({
    super.key,
    this.fullScreen,
    this.routes,
    required this.child,
  });

  /// Observes the root navigator, so that toolbars in nested navigators
  /// learn about routes pushed over them there (e.g. a dialog).
  final TitlebarRouteObserver? routes;

  /// Whether the window is in full screen; by default from the window
  /// delegate of macos_window_utils.
  final ValueListenable<bool>? fullScreen;
  final Widget child;

  @override
  State<TitlebarScope> createState() => _TitlebarScopeState();
}

class _TitlebarScopeState extends State<TitlebarScope> {
  _FullScreenDelegate? _delegate;
  NSWindowDelegateHandle? _handle;

  ValueListenable<bool>? get _fullScreen => widget.fullScreen ?? _delegate;

  @override
  void initState() {
    super.initState();
    if (_isMac && widget.fullScreen == null) {
      _delegate = _FullScreenDelegate();
      _handle = WindowManipulator.addNSWindowDelegate(_delegate!);
      // The window can start in full screen, before any delegate event.
      WindowManipulator.isWindowFullscreened().then((full) {
        if (mounted) _delegate?.seed(full);
      }, onError: (Object _) {});
    }
  }

  @override
  void dispose() {
    _handle?.removeFromHandler();
    _delegate?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final fullScreen = _fullScreen;
    final child = _TitlebarRoutes(routes: widget.routes, child: widget.child);
    if (!_isMac || fullScreen == null) {
      return TitlebarInsets(left: 0, child: child);
    }
    return ValueListenableBuilder<bool>(
      valueListenable: fullScreen,
      builder: (context, full, child) =>
          TitlebarInsets(left: full ? 0 : kTrafficLightsInset, child: child!),
      child: child,
    );
  }
}

/// Notifies when the routes of the navigator it observes change.
class TitlebarRouteObserver extends NavigatorObserver with ChangeNotifier {
  bool _scheduled = false;
  bool _disposed = false;

  // Calls can arrive mid-build (initial routes); then notify after the frame.
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
  void didPush(Route<dynamic> route, Route<dynamic>? previousRoute) =>
      _changed();

  @override
  void didPop(Route<dynamic> route, Route<dynamic>? previousRoute) =>
      _changed();

  @override
  void didRemove(Route<dynamic> route, Route<dynamic>? previousRoute) =>
      _changed();

  @override
  void didReplace({Route<dynamic>? newRoute, Route<dynamic>? oldRoute}) =>
      _changed();
}

class _TitlebarRoutes extends InheritedWidget {
  const _TitlebarRoutes({required this.routes, required super.child});

  final TitlebarRouteObserver? routes;

  static TitlebarRouteObserver? of(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<_TitlebarRoutes>()?.routes;

  @override
  bool updateShouldNotify(_TitlebarRoutes oldWidget) =>
      routes != oldWidget.routes;
}

class _FullScreenDelegate extends NSWindowDelegate
    implements ValueListenable<bool> {
  final _value = ValueNotifier(false);
  bool _hadEvent = false;

  @override
  bool get value => _value.value;

  @override
  void addListener(VoidCallback listener) => _value.addListener(listener);

  @override
  void removeListener(VoidCallback listener) => _value.removeListener(listener);

  void dispose() => _value.dispose();

  /// The state at start; a delegate event that came first wins.
  void seed(bool fullScreen) {
    if (!_hadEvent) _value.value = fullScreen;
  }

  @override
  void windowWillEnterFullScreen() {
    _hadEvent = true;
    _value.value = true;
  }

  @override
  void windowDidExitFullScreen() {
    _hadEvent = true;
    _value.value = false;
  }
}

/// An [AppBar] that, on macOS, is the unified toolbar row: 52 px high, under
/// the traffic lights, with its content after [TitlebarInsets]. Empty space
/// in the row drags the window natively; interactive children pass clicks
/// through the native toolbar to Flutter.
///
/// On macOS the leading widget, the actions, and a [title] that is not a
/// plain [Text] pass clicks through, so they do not drag the window. A plain
/// [Text] title drags it, like a native title.
class ArgusAppBar extends StatelessWidget implements PreferredSizeWidget {
  const ArgusAppBar({
    super.key,
    this.leading,
    this.automaticallyImplyLeading = true,
    this.title,
    this.actions,
    this.backgroundColor,
    this.titleSpacing,
  });

  final Widget? leading;
  final bool automaticallyImplyLeading;
  final Widget? title;
  final List<Widget>? actions;
  final Color? backgroundColor;
  final double? titleSpacing;

  @override
  Size get preferredSize => _isMac
      ? const Size.fromHeight(kMacToolbarHeight)
      // Keeps the theme's toolbar height, as for a plain AppBar.
      : AppBar().preferredSize;

  @override
  Widget build(BuildContext context) {
    if (!_isMac) {
      return AppBar(
        leading: leading,
        automaticallyImplyLeading: automaticallyImplyLeading,
        title: title,
        actions: actions,
        backgroundColor: backgroundColor,
        titleSpacing: titleSpacing,
      );
    }
    final lead =
        leading ??
        (automaticallyImplyLeading ? _impliedLeading(context) : null);
    final head = title;
    final color =
        backgroundColor ??
        AppBarTheme.of(context).backgroundColor ??
        Theme.of(context).colorScheme.surface;
    return MacosToolbarPassthroughScope(
      child: _MacToolbar(
        child: ColoredBox(
          color: color,
          child: Padding(
            padding: EdgeInsets.only(left: TitlebarInsets.of(context)),
            child: AppBar(
              primary: false,
              toolbarHeight: kMacToolbarHeight,
              automaticallyImplyLeading: false,
              leading: lead == null ? null : _passthrough(lead),
              // A plain text title stays draggable, like a native title.
              title: head == null || head is Text ? head : _passthrough(head),
              actions: actions?.map(_passthrough).toList(),
              backgroundColor: color,
              titleSpacing: titleSpacing,
            ),
          ),
        ),
      ),
    );
  }

  static Widget _passthrough(Widget child) => _RoutePassthrough(child: child);

  // What AppBar implies; it is built here so that it can pass through.
  static Widget? _impliedLeading(BuildContext context) {
    if (Scaffold.maybeOf(context)?.hasDrawer ?? false) {
      return const DrawerButton();
    }
    final route = ModalRoute.of(context);
    if (!(route?.impliesAppBarDismissal ?? false)) return null;
    return route is PageRoute && route.fullscreenDialog
        ? const CloseButton()
        : const BackButton();
  }
}

/// Manages the passthrough of a macOS toolbar row by the state of its route
/// and the routes enclosing its navigator:
/// - onstage with nothing over it: each interactive child passes through;
/// - a popup or dialog over it: the whole row passes through, so that the
///   barrier and menu items over the row receive clicks;
/// - covered by an opaque route (offstage): nothing passes through, so the
///   toolbar of the route above drags the window.
///
/// The rects are measured with the transitions' transforms, and a paint-only
/// transition does not lay the toolbar out again, so they are re-sent when a
/// transition of any of these routes ends.
class _MacToolbar extends StatefulWidget {
  const _MacToolbar({required this.child});

  final Widget child;

  @override
  State<_MacToolbar> createState() => _MacToolbarState();
}

class _MacToolbarState extends State<_MacToolbar> {
  final _rowKey = GlobalKey();
  List<ModalRoute<dynamic>> _routes = const [];
  TitlebarRouteObserver? _observer;
  VoidCallback? _notify;
  bool? _wasRow;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _notify = MacosToolbarPassthroughScope.maybeNotifyChangesOf(context);
    final observer = _TitlebarRoutes.of(context);
    if (observer != _observer) {
      _observer?.removeListener(_rebuild);
      _observer = observer?..addListener(_rebuild);
    }
    final routes = _enclosingRoutes(context);
    if (listEquals(routes, _routes)) return;
    _unlisten();
    _routes = routes;
    for (final route in routes) {
      route.animation?.addStatusListener(_onAnimation);
      route.secondaryAnimation?.addStatusListener(_onSecondaryAnimation);
    }
  }

  // This route, then the route of each enclosing navigator.
  static List<ModalRoute<dynamic>> _enclosingRoutes(BuildContext context) {
    final routes = <ModalRoute<dynamic>>[];
    var route = ModalRoute.of(context);
    while (route != null) {
      routes.add(route);
      final outer = route.navigator?.context;
      route = outer == null ? null : ModalRoute.of(outer);
    }
    return routes;
  }

  @override
  void dispose() {
    _unlisten();
    _observer?.removeListener(_rebuild);
    super.dispose();
  }

  void _unlisten() {
    for (final route in _routes) {
      route.animation?.removeStatusListener(_onAnimation);
      route.secondaryAnimation?.removeStatusListener(_onSecondaryAnimation);
    }
  }

  void _rebuild() {
    if (mounted) setState(() {});
  }

  // Pushed and shown.
  void _onAnimation(AnimationStatus status) {
    if (status.isCompleted) _notify?.call();
  }

  // Covered, or shown again after the route above it popped.
  void _onSecondaryAnimation(AnimationStatus status) {
    _rebuild();
    if (status.isDismissed) _notify?.call();
  }

  @override
  Widget build(BuildContext context) {
    final onstage = TickerMode.valuesOf(context).enabled;
    final row = onstage && _routes.any((r) => !r.isCurrent);
    if (row != _wasRow) {
      _wasRow = row;
      // The new passthrough views are measured after the frame.
      SchedulerBinding.instance.addPostFrameCallback((_) => _notify?.call());
    }
    final bar = KeyedSubtree(
      key: _rowKey,
      child: _PassthroughButtons(enabled: onstage && !row, child: widget.child),
    );
    return row ? MacosToolbarPassthrough(child: bar) : bar;
  }
}

class _PassthroughButtons extends InheritedWidget {
  const _PassthroughButtons({required this.enabled, required super.child});

  final bool enabled;

  static bool of(BuildContext context) =>
      context
          .dependOnInheritedWidgetOfExactType<_PassthroughButtons>()
          ?.enabled ??
      true;

  @override
  bool updateShouldNotify(_PassthroughButtons oldWidget) =>
      enabled != oldWidget.enabled;
}

/// [MacosToolbarPassthrough] while its toolbar passes buttons through. The
/// child keeps its state when that changes.
class _RoutePassthrough extends StatefulWidget {
  const _RoutePassthrough({required this.child});

  final Widget child;

  @override
  State<_RoutePassthrough> createState() => _RoutePassthroughState();
}

class _RoutePassthroughState extends State<_RoutePassthrough> {
  final _childKey = GlobalKey();

  @override
  Widget build(BuildContext context) {
    final child = KeyedSubtree(key: _childKey, child: widget.child);
    return _PassthroughButtons.of(context)
        ? MacosToolbarPassthrough(child: child)
        : child;
  }
}

/// The top of a screen without an app bar: on macOS, a 52 px area of the
/// toolbar row that drags the window natively; elsewhere nothing.
class TitlebarSpacer extends StatelessWidget {
  const TitlebarSpacer({super.key});

  @override
  Widget build(BuildContext context) => _isMac
      ? const SizedBox(height: kMacToolbarHeight, width: double.infinity)
      : const SizedBox.shrink();
}
