import 'dart:math';

import 'package:flutter/foundation.dart';
import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';

/// Tells screens inside the home shell how to open and close the modal
/// projects drawer. Both callbacks are null when the drawer is a fixed side
/// panel.
class ShellDrawerScope extends InheritedWidget {
  const ShellDrawerScope({
    super.key,
    this.openDrawer,
    this.closeDrawer,
    required super.child,
  });

  final VoidCallback? openDrawer;
  final VoidCallback? closeDrawer;

  static VoidCallback? openDrawerOf(BuildContext context) => context
      .dependOnInheritedWidgetOfExactType<ShellDrawerScope>()
      ?.openDrawer;

  static VoidCallback? closeDrawerOf(BuildContext context) =>
      context.getInheritedWidgetOfExactType<ShellDrawerScope>()?.closeDrawer;

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

// Material's DrawerController values.
const double _kEdgeDragWidth = 20;
const double _kMinFlingVelocity = 365;
const Duration _kSettleDuration = Duration(milliseconds: 246);

// Android gesture navigation drops a back swipe that is held still for more
// than 250 ms (EdgeBackGestureHandler), so a longer hold reaches the app alone.
const Duration _kPeekHold = Duration(milliseconds: 300);
const Duration _kPeekDuration = Duration(milliseconds: 150);

/// A modal drawer over [child]. A hold at the edge peeks the drawer and a pull
/// then drags it open, like Android's DrawerLayout, so a quick edge swipe stays
/// with the system or route back gesture.
class ModalShellDrawer extends StatefulWidget {
  const ModalShellDrawer({
    super.key,
    required this.width,
    required this.drawer,
    required this.child,
  });

  final double width;
  final Widget drawer;
  final Widget child;

  @override
  State<ModalShellDrawer> createState() => ModalShellDrawerState();
}

class ModalShellDrawerState extends State<ModalShellDrawer>
    with SingleTickerProviderStateMixin {
  late final AnimationController _controller =
      AnimationController(duration: _kSettleDuration, vsync: this)
        ..addListener(() => setState(() {}))
        ..addStatusListener(_onStatus);
  final _focusScope = FocusScopeNode();
  final _overlayKey = GlobalKey();
  LocalHistoryEntry? _historyEntry;
  bool _edgeActive = false;
  double _peek = 0;

  bool get isOpen => _controller.status.isForwardOrCompleted;

  void open() => _controller.fling();

  void close() => _controller.fling(velocity: -1);

  @override
  void dispose() {
    _historyEntry?.remove();
    _controller.dispose();
    _focusScope.dispose();
    super.dispose();
  }

  // The route entry makes system back close the drawer, as in Scaffold.
  void _onStatus(AnimationStatus status) {
    switch (status) {
      case AnimationStatus.forward:
        if (_historyEntry != null) return;
        final route = ModalRoute.of(context);
        if (route == null) return;
        _historyEntry = LocalHistoryEntry(
          onRemove: () {
            _historyEntry = null;
            close();
          },
          impliesAppBarDismissal: false,
        );
        route.addLocalHistoryEntry(_historyEntry!);
        FocusScope.of(context).setFirstFocus(_focusScope);
      case AnimationStatus.reverse:
        _historyEntry?.remove();
        _historyEntry = null;
      case AnimationStatus.dismissed:
      case AnimationStatus.completed:
        break;
    }
  }

  void _drag(DragUpdateDetails details) =>
      _controller.value += details.primaryDelta! / widget.width;

  void _settle(double velocity) {
    if (_controller.isDismissed) return;
    if (velocity.abs() >= _kMinFlingVelocity) {
      _controller.fling(velocity: velocity / widget.width);
    } else if (_controller.value < 0.5) {
      close();
    } else {
      open();
    }
  }

  void _endEdge(double velocity) {
    if (!_edgeActive) return;
    setState(() => _edgeActive = false);
    _settle(velocity);
  }

  void _peekStart(double edge) {
    setState(() => _edgeActive = true);
    _peek = edge;
    _controller.animateTo(edge / widget.width, duration: _kPeekDuration);
  }

  void _peekPull(LongPressMoveUpdateDetails details) =>
      _controller.value = (_peek + details.offsetFromOrigin.dx) / widget.width;

  Widget _edgeStrip(double edge) => Listener(
    behavior: HitTestBehavior.translucent,
    // The recognizer reports no end for a cancel after the hold.
    onPointerCancel: (_) => _endEdge(0),
    child: RawGestureDetector(
      behavior: HitTestBehavior.translucent,
      excludeFromSemantics: true,
      gestures: {
        LongPressGestureRecognizer:
            GestureRecognizerFactoryWithHandlers<LongPressGestureRecognizer>(
              () => LongPressGestureRecognizer(
                duration: _kPeekHold,
                debugOwner: this,
              ),
              (r) => r
                ..onLongPressStart = ((_) => _peekStart(edge))
                ..onLongPressMoveUpdate = _peekPull
                ..onLongPressEnd = ((d) =>
                    _endEdge(d.velocity.pixelsPerSecond.dx)),
            ),
      },
    ),
  );

  Widget _overlay(BuildContext context) {
    final scrim = DrawerTheme.of(context).scrimColor ?? Colors.black54;
    return GestureDetector(
      key: _overlayKey,
      excludeFromSemantics: true,
      onHorizontalDragDown: (_) => _controller.stop(),
      onHorizontalDragUpdate: _drag,
      onHorizontalDragEnd: (d) => _settle(d.primaryVelocity ?? 0),
      onHorizontalDragCancel: () {
        if (!_controller.isAnimating) _settle(0);
      },
      child: RepaintBoundary(
        child: Stack(
          children: [
            BlockSemantics(
              child: ExcludeSemantics(
                // Android dismisses a modal with back.
                excluding: defaultTargetPlatform == TargetPlatform.android,
                child: GestureDetector(
                  onTap: close,
                  child: Semantics(
                    label: MaterialLocalizations.of(
                      context,
                    ).modalBarrierDismissLabel,
                    child: ColoredBox(
                      color: scrim.withValues(
                        alpha: scrim.a * _controller.value,
                      ),
                      child: const SizedBox.expand(),
                    ),
                  ),
                ),
              ),
            ),
            Align(
              alignment: Alignment.centerLeft,
              child: Align(
                alignment: Alignment.centerRight,
                widthFactor: _controller.value,
                child: RepaintBoundary(
                  child: FocusScope(
                    node: _focusScope,
                    child: ListTileTheme.merge(
                      style: ListTileStyle.drawer,
                      child: Drawer(width: widget.width, child: widget.drawer),
                    ),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final edge = max(
      MediaQuery.systemGestureInsetsOf(context).left,
      _kEdgeDragWidth + MediaQuery.paddingOf(context).left,
    );
    return Stack(
      fit: StackFit.expand,
      children: [
        widget.child,
        if (!_controller.isDismissed) _overlay(context),
        // Kept through its own gesture, which the peek outlives.
        if (_controller.isDismissed || _edgeActive)
          Positioned(
            key: const ValueKey('edge'),
            left: 0,
            top: 0,
            bottom: 0,
            width: edge,
            child: _edgeStrip(edge),
          ),
      ],
    );
  }
}
