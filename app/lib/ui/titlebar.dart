import 'dart:math' as math;

import 'package:flutter/cupertino.dart';
import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/scheduler.dart';
import 'package:macos_window_utils/macos/ns_window_delegate.dart';
import 'package:macos_window_utils/macos_window_utils.dart';
import 'package:macos_window_utils/widgets/macos_toolbar_passthrough.dart';

import 'theme.dart';

/// The height of the unified toolbar row on macOS; the traffic lights are
/// centered in it.
const double kMacToolbarHeight = 52;

/// The space at the left of the toolbar row that the traffic lights take.
const double kTrafficLightsInset = 80;

/// The space the sidebar toggle takes after the traffic lights: the 28 px
/// button and the gaps around it.
const double kSidebarToggleWidth = 36;

/// A light tint over the macOS sidebar vibrancy, so text contrast stays.
const Color kSidebarTint = Color(0x14000000);

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

  /// Tells the macOS toolbars to measure their passthrough again after they
  /// moved without a route transition or a resize (e.g. in a drawer that
  /// slid open).
  static void toolbarsMoved(BuildContext context) =>
      context.getInheritedWidgetOfExactType<_TitlebarRoutes>()?.moved.notify();

  @override
  State<TitlebarScope> createState() => _TitlebarScopeState();
}

class _Moved extends ChangeNotifier {
  void notify() => notifyListeners();
}

class _TitlebarScopeState extends State<TitlebarScope> {
  final _moved = _Moved();
  FullScreenDelegate? _delegate;
  NSWindowDelegateHandle? _handle;

  ValueListenable<bool>? get _fullScreen => widget.fullScreen ?? _delegate;

  @override
  void initState() {
    super.initState();
    if (_isMac && widget.fullScreen == null) {
      _delegate = FullScreenDelegate();
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
    _moved.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final fullScreen = _fullScreen;
    final child = _TitlebarRoutes(
      routes: widget.routes,
      moved: _moved,
      child: widget.child,
    );
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
  final _routes = <Route<dynamic>>[];
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

  /// The observed route whose page contains [context], found without a
  /// dependency on that route.
  ModalRoute<dynamic>? routeAround(BuildContext context) {
    final pages = <BuildContext, ModalRoute<dynamic>>{
      for (final route in _routes.whereType<ModalRoute<dynamic>>())
        ?route.subtreeContext: route,
    };
    ModalRoute<dynamic>? found;
    context.visitAncestorElements((element) {
      found = pages[element];
      return found == null;
    });
    return found;
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
    if (i >= 0 && newRoute != null) _routes[i] = newRoute;
    _changed();
  }
}

class _TitlebarRoutes extends InheritedWidget {
  const _TitlebarRoutes({
    required this.routes,
    required this.moved,
    required super.child,
  });

  final TitlebarRouteObserver? routes;
  final _Moved moved;

  static _TitlebarRoutes? of(BuildContext context) =>
      context.dependOnInheritedWidgetOfExactType<_TitlebarRoutes>();

  @override
  bool updateShouldNotify(_TitlebarRoutes oldWidget) =>
      routes != oldWidget.routes || moved != oldWidget.moved;
}

@visibleForTesting
class FullScreenDelegate extends NSWindowDelegate
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

  // The traffic lights come back as the exit starts.
  @override
  void windowWillExitFullScreen() {
    _hadEvent = true;
    _value.value = false;
  }

  @override
  void windowDidExitFullScreen() {
    _hadEvent = true;
    _value.value = false;
  }
}

/// The tabs of the page below, which [ArgusAppBar] shows on macOS as a
/// segmented control in the center of its row: labels where they fit whole,
/// else icons with the labels as tooltips.
class ToolbarTabs extends InheritedWidget {
  const ToolbarTabs({
    super.key,
    required this.labels,
    required this.icons,
    required this.selected,
    required this.onSelected,
    required super.child,
  });

  final List<String> labels;

  /// One per label, shown instead of the labels where they do not fit.
  final List<Widget> icons;
  final int selected;
  final ValueChanged<int> onSelected;

  /// The tabs for a toolbar at [context]; null where there are none, or where
  /// a route was pushed above them (the toolbar belongs to that route).
  static ToolbarTabs? maybeOf(BuildContext context) {
    final tabs = context.dependOnInheritedWidgetOfExactType<ToolbarTabs>();
    final page = ModalRoute.of(context)?.subtreeContext;
    if (tabs == null || page == null) return tabs;
    var onPage = false;
    context
        .getElementForInheritedWidgetOfExactType<ToolbarTabs>()!
        .visitAncestorElements((element) {
          onPage = element == page;
          return !onPage;
        });
    return onPage ? tabs : null;
  }

  @override
  bool updateShouldNotify(ToolbarTabs oldWidget) =>
      selected != oldWidget.selected ||
      onSelected != oldWidget.onSelected ||
      !listEquals(labels, oldWidget.labels) ||
      !listEquals(icons, oldWidget.icons);
}

/// The name of the current tab as a toolbar title: shown as [Text], and
/// omitted where the toolbar shows the tabs, like a plain [Text] title.
class TabTitle extends StatelessWidget {
  const TabTitle(this.data, {super.key});

  final String data;

  @override
  Widget build(BuildContext context) =>
      _isMac && ToolbarTabs.maybeOf(context) != null
      ? const SizedBox.shrink()
      : Text(data);
}

class _TabsControl extends StatefulWidget {
  const _TabsControl({required this.tabs, required this.link});

  final ToolbarTabs tabs;
  final _TabsSwitchLink link;

  @override
  State<_TabsControl> createState() => _TabsControlState();
}

class _TabsControlState extends State<_TabsControl> {
  // Which control the switch shows; the others are kept out of focus.
  _TabsShown _shown = _TabsShown.labels;
  _TabsShown _laidOut = _TabsShown.labels;

  void _onShown(_TabsShown shown) {
    _laidOut = shown;
    if (shown == _shown) return;
    SchedulerBinding.instance.addPostFrameCallback((_) {
      if (mounted && _laidOut != _shown) {
        setState(() => _shown = _laidOut);
      }
    });
  }

  @override
  void didUpdateWidget(_TabsControl oldWidget) {
    super.didUpdateWidget(oldWidget);
    final tabs = widget.tabs;
    final old = oldWidget.tabs;
    // The control and the toolbar children around it may have moved without
    // new constraints, which alone re-measure a passthrough.
    if (tabs.selected != old.selected || !listEquals(tabs.labels, old.labels)) {
      final notify = MacosToolbarPassthroughScope.maybeNotifyChangesOf(context);
      SchedulerBinding.instance.addPostFrameCallback((_) => notify?.call());
    }
  }

  Widget _control(List<Widget> segments) {
    final tabs = widget.tabs;
    return CupertinoSlidingSegmentedControl<int>(
      groupValue: tabs.selected,
      backgroundColor: AppColors.card,
      thumbColor: AppColors.border,
      // The segments are at least 28 px high; without vertical padding, so is
      // the control.
      padding: const EdgeInsets.symmetric(horizontal: 3),
      onValueChanged: (i) {
        if (i != null) tabs.onSelected(i);
      },
      children: {for (var i = 0; i < segments.length; i++) i: segments[i]},
    );
  }

  @override
  Widget build(BuildContext context) {
    final tabs = widget.tabs;
    return _TabsSwitch(
      link: widget.link,
      onShown: _onShown,
      labels: ExcludeFocus(
        excluding: _shown != _TabsShown.labels,
        child: _control([
          for (final label in tabs.labels)
            Text(
              label,
              style: const TextStyle(color: AppColors.text, fontSize: 13),
            ),
        ]),
      ),
      icons: ExcludeFocus(
        excluding: _shown != _TabsShown.icons,
        child: _control([
          for (var i = 0; i < tabs.icons.length; i++)
            Tooltip(
              message: tabs.labels[i],
              child: IconTheme.merge(
                data: const IconThemeData(color: AppColors.text, size: 16),
                child: tabs.icons[i],
              ),
            ),
        ]),
      ),
    );
  }
}

/// The middle of a tabbed toolbar row: the row with the tabs control, which
/// share a [_TabsSwitchLink].
class _TabbedTitle extends StatefulWidget {
  const _TabbedTitle({
    required this.spacing,
    required this.leading,
    required this.title,
    required this.tabs,
    required this.actions,
  });

  final double spacing;
  final Widget? leading;
  final Widget? title;
  final ToolbarTabs tabs;
  final Widget? actions;

  @override
  State<_TabbedTitle> createState() => _TabbedTitleState();
}

class _TabbedTitleState extends State<_TabbedTitle> {
  final _link = _TabsSwitchLink();

  @override
  Widget build(BuildContext context) => SizedBox(
    height: kMacToolbarHeight,
    child: _TabbedToolbar(
      link: _link,
      spacing: widget.spacing,
      leading: widget.leading,
      title: widget.title,
      tabs: ArgusAppBar._passthrough(
        _TabsControl(tabs: widget.tabs, link: _link),
      ),
      actions: widget.actions,
    ),
  );
}

/// Lets the toolbar row read the width the icons need from the switch, which
/// is under a passthrough (a LayoutBuilder, without intrinsic sizes).
class _TabsSwitchLink {
  _RenderTabsSwitch? box;
}

/// Which control a [_TabsSwitch] shows.
enum _TabsShown { labels, icons, none }

/// Shows [labels] where it fits whole in the width given, else [icons] where
/// they fit whole, else nothing (0 wide; the tabs stay reachable by ⌘1–⌘4
/// and the View menu). A control not shown is neither painted nor hit, and
/// is offstage to finders and semantics; one that does not fit is not laid
/// out, as the segmented control cannot lay out below its minimum width.
class _TabsSwitch extends MultiChildRenderObjectWidget {
  _TabsSwitch({
    required this.link,
    required this.onShown,
    required Widget labels,
    required Widget icons,
  }) : super(children: [labels, icons]);

  final _TabsSwitchLink link;

  /// Called after each layout with the control shown.
  final ValueChanged<_TabsShown> onShown;

  @override
  _RenderTabsSwitch createRenderObject(BuildContext context) =>
      link.box = _RenderTabsSwitch(onShown);

  @override
  void updateRenderObject(
    BuildContext context,
    _RenderTabsSwitch renderObject,
  ) => link.box = renderObject..onShown = onShown;

  @override
  MultiChildRenderObjectElement createElement() => _TabsSwitchElement(this);
}

class _TabsSwitchElement extends MultiChildRenderObjectElement {
  _TabsSwitchElement(super.widget);

  @override
  void debugVisitOnstageChildren(ElementVisitor visitor) {
    final shown = (renderObject as _RenderTabsSwitch).shown;
    for (final child in children) {
      if (child.renderObject == shown) visitor(child);
    }
  }
}

class _SwitchParentData extends ContainerBoxParentData<RenderBox> {}

class _RenderTabsSwitch extends RenderBox
    with
        ContainerRenderObjectMixin<RenderBox, _SwitchParentData>,
        RenderBoxContainerDefaultsMixin<RenderBox, _SwitchParentData> {
  _RenderTabsSwitch(this.onShown);

  ValueChanged<_TabsShown> onShown;

  RenderBox? shown;

  /// The width of the icons, as of the last layout.
  double iconsWidth = 0;

  RenderBox get _labels => firstChild!;
  RenderBox get _icons => lastChild!;

  @override
  void setupParentData(RenderBox child) {
    if (child.parentData is! _SwitchParentData) {
      child.parentData = _SwitchParentData();
    }
  }

  RenderBox? _pick(BoxConstraints constraints) {
    final width = constraints.maxWidth;
    if (_labels.getMaxIntrinsicWidth(double.infinity) <= width) return _labels;
    if (_icons.getMaxIntrinsicWidth(double.infinity) <= width) return _icons;
    return null;
  }

  @override
  double computeMinIntrinsicWidth(double height) =>
      _icons.getMaxIntrinsicWidth(height);

  @override
  double computeMaxIntrinsicWidth(double height) =>
      _labels.getMaxIntrinsicWidth(height);

  @override
  double computeMinIntrinsicHeight(double width) => math.max(
    _labels.getMinIntrinsicHeight(width),
    _icons.getMinIntrinsicHeight(width),
  );

  @override
  double computeMaxIntrinsicHeight(double width) => math.max(
    _labels.getMaxIntrinsicHeight(width),
    _icons.getMaxIntrinsicHeight(width),
  );

  @override
  Size computeDryLayout(BoxConstraints constraints) {
    final pick = _pick(constraints);
    return constraints.constrain(
      pick?.getDryLayout(constraints.loosen()) ?? Size.zero,
    );
  }

  @override
  void performLayout() {
    final pick = _pick(constraints);
    iconsWidth = _icons.getMaxIntrinsicWidth(double.infinity);
    if (pick != shown) {
      shown = pick;
      markNeedsSemanticsUpdate();
    }
    // The segmented control cannot lay out under its minimum width, so
    // nothing is laid out where even the icons do not fit.
    final loose = constraints.loosen();
    if (pick != null) {
      _labels.layout(loose, parentUsesSize: pick == _labels);
      _icons.layout(loose, parentUsesSize: pick == _icons);
    }
    size = constraints.constrain(pick?.size ?? Size.zero);
    onShown(
      pick == _labels
          ? _TabsShown.labels
          : pick == _icons
          ? _TabsShown.icons
          : _TabsShown.none,
    );
  }

  @override
  void paint(PaintingContext context, Offset offset) {
    if (shown case final shown?) context.paintChild(shown, offset);
  }

  @override
  bool hitTestChildren(BoxHitTestResult result, {required Offset position}) =>
      shown?.hitTest(result, position: position) ?? false;

  @override
  void visitChildrenForSemantics(RenderObjectVisitor visitor) {
    if (shown case final shown?) visitor(shown);
  }
}

enum _Slot { leading, title, tabs, actions }

/// A toolbar row with [tabs] centered in it, between the [leading] and
/// [title] at the left and the [actions] at the right. The row is shared in
/// this order: the title gets up to [_TabbedToolbarLayout.titleReserve]; the
/// tabs get their labels' width if it fits, else their icons'; the title
/// takes what is left, shrinking under its reserve only for the icons.
class _TabbedToolbar
    extends SlottedMultiChildRenderObjectWidget<_Slot, RenderBox> {
  const _TabbedToolbar({
    required this.link,
    required this.spacing,
    required this.leading,
    required this.title,
    required this.tabs,
    required this.actions,
  });

  final _TabsSwitchLink link;
  final double spacing;
  final Widget? leading;
  final Widget? title;
  final Widget tabs;
  final Widget? actions;

  @override
  Iterable<_Slot> get slots => _Slot.values;

  @override
  Widget? childForSlot(_Slot slot) => switch (slot) {
    _Slot.leading => leading,
    _Slot.title => title,
    _Slot.tabs => tabs,
    _Slot.actions => actions,
  };

  @override
  _TabbedToolbarLayout createRenderObject(BuildContext context) =>
      _TabbedToolbarLayout(
        link,
        spacing,
        MacosToolbarPassthroughScope.maybeNotifyChangesOf(context),
      );

  @override
  void updateRenderObject(
    BuildContext context,
    _TabbedToolbarLayout renderObject,
  ) {
    renderObject
      ..link = link
      ..spacing = spacing
      ..onMoved = MacosToolbarPassthroughScope.maybeNotifyChangesOf(context);
  }
}

class _TabbedToolbarLayout extends RenderBox
    with SlottedContainerRenderObjectMixin<_Slot, RenderBox> {
  _TabbedToolbarLayout(this.link, this._spacing, this.onMoved);

  _TabsSwitchLink link;

  /// The width the title gets before the tabs.
  static const double titleReserve = 200;

  // As AppBar: the leading box is a square of the default toolbar height.
  static const double _leadingWidth = kToolbarHeight;

  double _spacing;
  set spacing(double value) {
    if (value == _spacing) return;
    _spacing = value;
    markNeedsLayout();
  }

  /// Called after a frame in which a child moved or resized; the passthrough
  /// rects are measured again then.
  VoidCallback? onMoved;

  List<Rect>? _rects;

  RenderBox? get _leading => childForSlot(_Slot.leading);
  RenderBox? get _title => childForSlot(_Slot.title);
  RenderBox get _tabs => childForSlot(_Slot.tabs)!;
  RenderBox? get _actions => childForSlot(_Slot.actions);

  Offset _offsetOf(RenderBox child) =>
      (child.parentData! as BoxParentData).offset;

  void _place(RenderBox child, Offset offset) =>
      (child.parentData! as BoxParentData).offset = offset;

  @override
  Size computeDryLayout(BoxConstraints constraints) => constraints.biggest;

  @override
  void performLayout() {
    size = constraints.biggest;
    final height = size.height;
    final spacing = _spacing;
    var start = 0.0;
    if (_leading case final leading?) {
      leading.layout(
        BoxConstraints.tightFor(width: _leadingWidth, height: height),
        parentUsesSize: true,
      );
      _place(leading, Offset.zero);
      start = _leadingWidth;
    }
    var end = size.width;
    if (_actions case final actions?) {
      actions.layout(
        BoxConstraints(
          maxWidth: size.width,
          minHeight: height,
          maxHeight: height,
        ),
        parentUsesSize: true,
      );
      end -= actions.size.width;
      _place(actions, Offset(end, 0));
    }
    final left = start + spacing;
    final right = end - spacing;
    final title = _title;
    // The title's share first; an empty one (e.g. a TabTitle where the tabs
    // show) takes no space.
    var reserve = 0.0;
    if (title != null) {
      title.layout(
        BoxConstraints(
          maxWidth: math.max(0, math.min(titleReserve, right - left)),
        ),
        parentUsesSize: true,
      );
      reserve = title.size.width;
    }
    final hasTitle = reserve > 0;
    final tabsLeft = hasTitle ? left + reserve + spacing : left;
    final tabs = _tabs;
    tabs.layout(
      BoxConstraints(
        maxWidth: math.max(0, right - tabsLeft),
        maxHeight: height,
      ),
      parentUsesSize: true,
    );
    // The icons do not fit beside the title's share: the title gives way.
    final iconsWidth = link.box?.iconsWidth ?? 0;
    if (tabs.size.width < iconsWidth && right - tabsLeft < right - left) {
      tabs.layout(
        BoxConstraints(
          maxWidth: math.max(0, math.min(iconsWidth, right - left)),
          maxHeight: height,
        ),
        parentUsesSize: true,
      );
    }
    final tabsWidth = tabs.size.width;
    if (title != null) {
      title.layout(
        BoxConstraints(
          maxWidth: hasTitle
              ? math.max(0, right - tabsWidth - spacing - left)
              : 0,
        ),
        parentUsesSize: true,
      );
      _place(title, Offset(left, (height - title.size.height) / 2));
    }
    final tabsMin = hasTitle ? left + title!.size.width + spacing : left;
    final centered = (size.width - tabsWidth) / 2;
    // Off center only as far as the title needs, and never past the ends.
    final x = math.max(
      math.min(math.max(centered, tabsMin), right - tabsWidth),
      start,
    );
    _place(tabs, Offset(x, (height - tabs.size.height) / 2));

    final rects = [for (final child in children) _offsetOf(child) & child.size];
    if (_rects != null && !listEquals(rects, _rects)) {
      SchedulerBinding.instance.addPostFrameCallback((_) => onMoved?.call());
    }
    _rects = rects;
  }

  @override
  void paint(PaintingContext context, Offset offset) {
    for (final child in children) {
      context.paintChild(child, offset + _offsetOf(child));
    }
  }

  @override
  bool hitTestChildren(BoxHitTestResult result, {required Offset position}) {
    for (final child in children.toList().reversed) {
      final hit = result.addWithPaintOffset(
        offset: _offsetOf(child),
        position: position,
        hitTest: (result, transformed) =>
            child.hitTest(result, position: transformed),
      );
      if (hit) return true;
    }
    return false;
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
///
/// Under [ToolbarTabs] of its own route, on macOS, it shows the tabs in the
/// center of the row and omits a plain [Text] title.
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
    final tabs = ToolbarTabs.maybeOf(context);
    return MacosToolbarPassthroughScope(
      child: _MacToolbar(
        child: ColoredBox(
          color: color,
          child: Padding(
            padding: EdgeInsets.only(left: TitlebarInsets.of(context)),
            child: tabs == null
                ? AppBar(
                    primary: false,
                    toolbarHeight: kMacToolbarHeight,
                    automaticallyImplyLeading: false,
                    leading: lead == null ? null : _passthrough(lead),
                    // A plain text title stays draggable, like a native title.
                    title: head == null || head is Text
                        ? head
                        : _passthrough(head),
                    actions: actions?.map(_passthrough).toList(),
                    backgroundColor: color,
                    titleSpacing: titleSpacing,
                  )
                // The whole row is laid out here, so that the tabs are
                // centered in it.
                : AppBar(
                    primary: false,
                    toolbarHeight: kMacToolbarHeight,
                    automaticallyImplyLeading: false,
                    backgroundColor: color,
                    titleSpacing: 0,
                    // Only the title is a header, not the whole row.
                    excludeHeaderSemantics: true,
                    title: _TabbedTitle(
                      spacing:
                          titleSpacing ??
                          AppBarTheme.of(context).titleSpacing ??
                          NavigationToolbar.kMiddleSpacing,
                      leading: lead == null ? null : _passthrough(lead),
                      // The tabs name the page, so a plain text title goes.
                      title: head == null || head is Text
                          ? null
                          : Semantics(header: true, child: _passthrough(head)),
                      tabs: tabs,
                      actions: actions == null || actions!.isEmpty
                          ? null
                          : _withActionsIconTheme(
                              context,
                              Row(
                                mainAxisSize: MainAxisSize.min,
                                children: actions!.map(_passthrough).toList(),
                              ),
                            ),
                    ),
                  ),
          ),
        ),
      ),
    );
  }

  static Widget _passthrough(Widget child) => _RoutePassthrough(child: child);

  // The icon theme AppBar gives its actions; the leading gets the theme of
  // the whole toolbar, which the title slot has too.
  static Widget _withActionsIconTheme(BuildContext context, Widget actions) {
    final theme = Theme.of(context);
    final bar = AppBarTheme.of(context);
    final data =
        bar.actionsIconTheme ??
        bar.iconTheme ??
        (theme.useMaterial3
            ? IconThemeData(
                color:
                    bar.foregroundColor ?? theme.colorScheme.onSurfaceVariant,
                size: 24,
              )
            : null);
    return data == null ? actions : IconTheme.merge(data: data, child: actions);
  }

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
/// and the route enclosing its navigator on the root navigator:
/// - onstage with nothing over it: each interactive child passes through;
/// - a popup or dialog over it: the row after [TitlebarInsets] passes
///   through, so that the barrier and menu items over the row receive clicks
///   and the traffic lights still work;
/// - covered by an opaque route (offstage) or in a hidden [IndexedStack]
///   child: nothing passes through, so the toolbar shown there drags the
///   window.
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
  List<ModalRoute<dynamic>> _routes = const [];
  TitlebarRouteObserver? _observer;
  _Moved? _moved;
  VoidCallback? _notify;
  bool? _wasRow;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _notify = MacosToolbarPassthroughScope.maybeNotifyChangesOf(context);
    final scope = _TitlebarRoutes.of(context);
    final observer = scope?.routes;
    if (observer != _observer) {
      _observer?.removeListener(_onRoutes);
      _observer = observer?..addListener(_onRoutes);
    }
    final moved = scope?.moved;
    if (moved != _moved) {
      _moved?.removeListener(_onMoved);
      _moved = moved?..addListener(_onMoved);
    }
    _track();
  }

  void _track() {
    final routes = _enclosingRoutes();
    if (listEquals(routes, _routes)) return;
    _unlisten();
    _routes = routes;
    for (final route in routes) {
      route.animation?.addStatusListener(_onAnimation);
      route.secondaryAnimation?.addStatusListener(_onSecondaryAnimation);
    }
  }

  // This route, then, in a nested navigator, the root route around it. The
  // root route is looked up without a dependency: one on the nested
  // navigator would rebuild all its pages on each root route change.
  List<ModalRoute<dynamic>> _enclosingRoutes() {
    final route = ModalRoute.of(context);
    if (route == null) return const [];
    final navigator = route.navigator;
    final root = _observer;
    if (navigator == null || root == null || navigator == root.navigator) {
      return [route];
    }
    return [route, ?root.routeAround(navigator.context)];
  }

  @override
  void dispose() {
    _unlisten();
    _observer?.removeListener(_onRoutes);
    _moved?.removeListener(_onMoved);
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

  void _onMoved() => _notify?.call();

  void _onRoutes() {
    if (!mounted) return;
    _track();
    _rebuild();
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
    final onstage =
        TickerMode.valuesOf(context).enabled && Visibility.of(context);
    final row = onstage && _routes.any((r) => !r.isCurrent);
    if (row != _wasRow) {
      _wasRow = row;
      // The new passthrough views are measured after the frame.
      SchedulerBinding.instance.addPostFrameCallback((_) => _notify?.call());
    }
    return Stack(
      alignment: Alignment.topLeft,
      children: [
        _PassthroughButtons(enabled: onstage && !row, child: widget.child),
        if (row)
          Positioned(
            left: TitlebarInsets.of(context),
            top: 0,
            right: 0,
            bottom: 0,
            // Empty, so Flutter hit tests go to the row under it.
            child: const MacosToolbarPassthrough(child: SizedBox.expand()),
          ),
      ],
    );
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
  Size? _size;
  bool _empty = false;

  // An empty child (e.g. a TabTitle where the tabs show) passes nothing
  // through; its size is known after layout, so this follows a frame later.
  void _onSize(Size size) {
    _size = size;
    if (size.isEmpty == _empty) return;
    SchedulerBinding.instance.addPostFrameCallback((_) {
      final empty = _size!.isEmpty;
      if (mounted && empty != _empty) setState(() => _empty = empty);
    });
  }

  @override
  Widget build(BuildContext context) {
    final child = KeyedSubtree(
      key: _childKey,
      child: _SizeObserver(onSize: _onSize, child: widget.child),
    );
    return _PassthroughButtons.of(context) && !_empty
        ? MacosToolbarPassthrough(child: child)
        : child;
  }
}

class _SizeObserver extends SingleChildRenderObjectWidget {
  const _SizeObserver({required this.onSize, super.child});

  final ValueChanged<Size> onSize;

  @override
  _RenderSizeObserver createRenderObject(BuildContext context) =>
      _RenderSizeObserver(onSize);

  @override
  void updateRenderObject(
    BuildContext context,
    _RenderSizeObserver renderObject,
  ) => renderObject.onSize = onSize;
}

class _RenderSizeObserver extends RenderProxyBox {
  _RenderSizeObserver(this.onSize);

  ValueChanged<Size> onSize;

  @override
  void performLayout() {
    super.performLayout();
    onSize(size);
  }
}

/// The sidebar toggle in the macOS toolbar row, beside the traffic lights:
/// a 52 px row from the window's left edge to [TitlebarInsets] plus
/// [kSidebarToggleWidth], with the button 4 px after the inset. It passes
/// clicks through like the buttons of an [ArgusAppBar].
class SidebarToggleBar extends StatelessWidget {
  const SidebarToggleBar({
    super.key,
    required this.shown,
    required this.onPressed,
  });

  /// Whether the sidebar is shown, which names the button's action.
  final bool shown;
  final VoidCallback onPressed;

  @override
  Widget build(BuildContext context) {
    final inset = TitlebarInsets.of(context);
    return MacosToolbarPassthroughScope(
      child: _MacToolbar(
        child: SizedBox(
          width: inset + kSidebarToggleWidth,
          height: kMacToolbarHeight,
          child: Padding(
            padding: EdgeInsets.only(left: inset + 4),
            child: Align(
              alignment: Alignment.centerLeft,
              child: ArgusAppBar._passthrough(
                IconButton(
                  icon: const Icon(Icons.view_sidebar_outlined),
                  tooltip: shown ? 'Hide Sidebar' : 'Show Sidebar',
                  iconSize: 18,
                  padding: EdgeInsets.zero,
                  constraints: const BoxConstraints.tightFor(
                    width: 28,
                    height: 28,
                  ),
                  style: const ButtonStyle(
                    tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  ),
                  onPressed: onPressed,
                ),
              ),
            ),
          ),
        ),
      ),
    );
  }
}

/// The top of a screen or a side panel without an app bar: on macOS, a 52 px
/// area of the toolbar row that drags the window natively, and passes clicks
/// through under a popup like [ArgusAppBar]; elsewhere nothing.
class TitlebarSpacer extends StatelessWidget {
  const TitlebarSpacer({super.key});

  @override
  Widget build(BuildContext context) => _isMac
      ? const MacosToolbarPassthroughScope(
          child: _MacToolbar(
            child: SizedBox(height: kMacToolbarHeight, width: double.infinity),
          ),
        )
      : const SizedBox.shrink();
}
