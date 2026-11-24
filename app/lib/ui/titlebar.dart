import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
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
  const TitlebarScope({super.key, this.fullScreen, required this.child});

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
    if (!_isMac || fullScreen == null) {
      return TitlebarInsets(left: 0, child: widget.child);
    }
    return ValueListenableBuilder<bool>(
      valueListenable: fullScreen,
      builder: (context, full, child) =>
          TitlebarInsets(left: full ? 0 : kTrafficLightsInset, child: child!),
      child: widget.child,
    );
  }
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
      child: _PassthroughRefresh(
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

/// Re-sends the passthrough rects of its [MacosToolbarPassthroughScope] when
/// the route settles: the rects are measured with the transition's transform,
/// and a paint-only transition does not lay the toolbar out again.
class _PassthroughRefresh extends StatefulWidget {
  const _PassthroughRefresh({required this.child});

  final Widget child;

  @override
  State<_PassthroughRefresh> createState() => _PassthroughRefreshState();
}

class _PassthroughRefreshState extends State<_PassthroughRefresh> {
  ModalRoute<dynamic>? _route;
  VoidCallback? _notify;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _notify = MacosToolbarPassthroughScope.maybeNotifyChangesOf(context);
    final route = ModalRoute.of(context);
    if (route == _route) return;
    _unlisten();
    _route = route;
    route?.animation?.addStatusListener(_onAnimation);
    route?.secondaryAnimation?.addStatusListener(_onSecondaryAnimation);
  }

  @override
  void dispose() {
    _unlisten();
    super.dispose();
  }

  void _unlisten() {
    _route?.animation?.removeStatusListener(_onAnimation);
    _route?.secondaryAnimation?.removeStatusListener(_onSecondaryAnimation);
  }

  // Pushed and shown.
  void _onAnimation(AnimationStatus status) {
    if (status.isCompleted) _notify?.call();
  }

  // Shown again after the route above it is popped.
  void _onSecondaryAnimation(AnimationStatus status) {
    if (status.isDismissed) _notify?.call();
  }

  @override
  Widget build(BuildContext context) => widget.child;
}

/// [MacosToolbarPassthrough] while its route is current and onstage. A
/// covered route removes its native passthrough views, so that the toolbar
/// of the route above it drags the window. The child keeps its state.
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
    final active =
        (ModalRoute.isCurrentOf(context) ?? true) &&
        TickerMode.valuesOf(context).enabled;
    final child = KeyedSubtree(key: _childKey, child: widget.child);
    return active ? MacosToolbarPassthrough(child: child) : child;
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
