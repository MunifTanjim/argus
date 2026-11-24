import 'package:flutter/foundation.dart' show defaultTargetPlatform, TargetPlatform;
import 'package:flutter/services.dart';
import 'package:flutter/widgets.dart';

import '../util/platform.dart';
import 'adaptive_nav.dart';

/// The composer of the current session registers its node; null when no
/// session is open.
final ValueNotifier<FocusNode?> activeComposerFocus = ValueNotifier(null);

/// live_emulator.dart adds its node when it is created and removes it in
/// dispose.
final Set<FocusNode> liveTerminalFocusNodes = {};

bool _isTextInput(FocusNode? node) {
  final ctx = node?.context;
  if (ctx == null) return false;
  return ctx.widget is EditableText ||
      ctx.findAncestorWidgetOfExactType<EditableText>() != null;
}

/// Whether the primary focus is inside an [EditableText].
bool textInputHasFocus() => _isTextInput(primaryFocus);

/// Moves focus to the next or previous traversable widget, unless that is a
/// text field: J and K at the end of a list stay on its last row. They also
/// stay at the edge of a route instead of leaving it.
void _moveFocusSkippingText({required bool forward}) {
  final current = primaryFocus;
  if (current == null) return;
  // A route's scope hands the edge to its parent scope's own policy, which
  // would skip the text field check below.
  final scope = current.nearestScope;
  final edge = scope?.traversalEdgeBehavior;
  if (edge == TraversalEdgeBehavior.parentScope) {
    scope!.traversalEdgeBehavior = TraversalEdgeBehavior.stop;
  }
  try {
    _policyMove(current, forward: forward);
  } finally {
    if (edge != null) scope!.traversalEdgeBehavior = edge;
  }
}

void _policyMove(FocusNode current, {required bool forward}) {
  final policy = ReadingOrderTraversalPolicy(
    requestFocusCallback:
        (node, {alignmentPolicy, alignment, duration, curve}) {
      if (_isTextInput(node)) return;
      FocusTraversalPolicy.defaultTraversalRequestFocusCallback(
        node,
        alignmentPolicy: alignmentPolicy,
        alignment: alignment,
        duration: duration,
        curve: curve,
      );
    },
  );
  forward ? policy.next(current) : policy.previous(current);
}

bool liveTerminalHasFocus() => liveTerminalFocusNodes.any((n) => n.hasFocus);

bool get _isMac => defaultTargetPlatform == TargetPlatform.macOS;

/// The bindings, for the shortcut sheet. Labels use ⌘ on macOS, Ctrl on Linux.
List<({String keys, String action})> shortcutDescriptions() {
  String mod(String key) => _isMac ? '⌘$key' : 'Ctrl+$key';
  return [
    (
      keys: mod('K'),
      action: _isMac
          ? 'Quick switcher (clears a focused terminal)'
          : 'Quick switcher',
    ),
    (keys: mod('N'), action: 'New session'),
    (keys: '${mod('1')} – ${mod('4')}', action: 'Switch tab'),
    (keys: mod('['), action: 'Back'),
    (keys: mod('L'), action: 'Focus the reply box'),
    (keys: mod(','), action: 'Settings'),
    if (_isMac) (keys: '⌃⌘S', action: 'Show or hide the sidebar'),
    (keys: '/', action: 'Focus the reply box'),
    (keys: 'Esc', action: 'Back'),
    (keys: 'J / K', action: 'Next / previous item'),
    (keys: '?', action: 'Keyboard shortcuts'),
  ];
}

const _tabKeys = [
  LogicalKeyboardKey.digit1,
  LogicalKeyboardKey.digit2,
  LogicalKeyboardKey.digit3,
  LogicalKeyboardKey.digit4,
];

/// App-wide keyboard shortcuts on desktop. A key the focused widget does not
/// handle bubbles up to here; a shortcut whose guard fails passes it on.
///
/// A focused terminal takes keys first. On Linux it keeps every Ctrl key; on
/// macOS the Cmd shortcuts and ⌃⌘S are taken before it, except Cmd+K, which
/// clears it.
class DesktopShortcuts extends StatefulWidget {
  const DesktopShortcuts({
    super.key,
    required this.child,
    required this.onBack,
    required this.onSpawn,
    required this.onSettings,
    required this.onQuickSwitcher,
    required this.onShortcutSheet,
    this.onToggleSidebar,
  });

  final Widget child;
  final VoidCallback onBack;
  final VoidCallback onSpawn;
  final VoidCallback onSettings;
  final VoidCallback onQuickSwitcher;
  final VoidCallback onShortcutSheet;

  /// Bound to ⌃⌘S on macOS only.
  final VoidCallback? onToggleSidebar;

  @override
  State<DesktopShortcuts> createState() => _DesktopShortcutsState();
}

class _DesktopShortcutsState extends State<DesktopShortcuts> {
  @override
  void initState() {
    super.initState();
    FocusManager.instance.addEarlyKeyEventHandler(_onEarlyKey);
  }

  @override
  void dispose() {
    FocusManager.instance.removeEarlyKeyEventHandler(_onEarlyKey);
    super.dispose();
  }

  // Single-key shortcuts never take a key from a text field or a terminal.
  bool get _guardB => !textInputHasFocus() && !liveTerminalHasFocus();

  static bool _popupOnTop() {
    final ctx = primaryFocus?.context;
    return ctx != null && ModalRoute.of(ctx) is PopupRoute;
  }

  // Null when no composer is mounted, so `/` and Mod+L pass the key on.
  static VoidCallback? _focusComposer() {
    final node = activeComposerFocus.value;
    return (node?.context?.mounted ?? false) ? node!.requestFocus : null;
  }

  static bool _modOnly() {
    final kb = HardwareKeyboard.instance;
    final meta = kb.isMetaPressed, ctrl = kb.isControlPressed;
    final mod = _isMac ? meta && !ctrl : ctrl && !meta;
    return mod && !kb.isAltPressed && !kb.isShiftPressed;
  }

  // ⌃⌘S, macOS only.
  static bool _isToggleSidebar(LogicalKeyboardKey k) {
    final kb = HardwareKeyboard.instance;
    return _isMac &&
        k == LogicalKeyboardKey.keyS &&
        kb.isMetaPressed &&
        kb.isControlPressed &&
        !kb.isAltPressed &&
        !kb.isShiftPressed;
  }

  VoidCallback? _modAction(LogicalKeyboardKey k) {
    final w = widget;
    if (k == LogicalKeyboardKey.keyK) return w.onQuickSwitcher;
    if (k == LogicalKeyboardKey.keyN) return w.onSpawn;
    if (k == LogicalKeyboardKey.bracketLeft) return w.onBack;
    if (k == LogicalKeyboardKey.keyL) return _focusComposer();
    if (k == LogicalKeyboardKey.comma) return w.onSettings;
    final tab = _tabKeys.indexOf(k);
    if (tab >= 0) return () => selectVisibleTab(tab);
    return null;
  }

  /// The action for [e] as it bubbles up from the focused widget, or null if
  /// no binding matches.
  VoidCallback? _match(KeyEvent e) {
    final kb = HardwareKeyboard.instance;
    final k = e.logicalKey;
    if (_modOnly()) {
      // Over a terminal, _onEarlyKey has taken the macOS ones already.
      return liveTerminalHasFocus() ? null : _modAction(k);
    }
    if (_isToggleSidebar(k)) {
      // Over a terminal, _onEarlyKey has taken it already.
      return liveTerminalHasFocus() || textInputHasFocus()
          ? null
          : widget.onToggleSidebar;
    }
    if (kb.isMetaPressed || kb.isControlPressed || kb.isAltPressed) return null;
    final shift = kb.isShiftPressed;
    final question = k == LogicalKeyboardKey.question ||
        (shift && k == LogicalKeyboardKey.slash);
    if (question) return _guardB ? widget.onShortcutSheet : null;
    if (shift || !_guardB) return null;
    if (k == LogicalKeyboardKey.slash) return _focusComposer();
    if (k == LogicalKeyboardKey.escape) {
      return _popupOnTop() ? null : widget.onBack;
    }
    if (k == LogicalKeyboardKey.keyJ) {
      return () => _moveFocusSkippingText(forward: true);
    }
    if (k == LogicalKeyboardKey.keyK) {
      return () => _moveFocusSkippingText(forward: false);
    }
    return null;
  }

  static KeyEventResult _run(KeyEvent e, VoidCallback? action) {
    if (e is KeyUpEvent || action == null) return KeyEventResult.ignored;
    // A held key repeats: swallow the repeats without acting again.
    if (e is KeyDownEvent) action();
    return KeyEventResult.handled;
  }

  KeyEventResult _onKey(FocusNode _, KeyEvent e) => _run(e, _match(e));

  // Keys taken by _onEarlyKey, so their release does not reach the terminal
  // either.
  final _earlyDown = <PhysicalKeyboardKey>{};

  // Runs before the focused terminal sees the key.
  KeyEventResult _onEarlyKey(KeyEvent e) {
    if (e is KeyUpEvent) {
      return _earlyDown.remove(e.physicalKey)
          ? KeyEventResult.handled
          : KeyEventResult.ignored;
    }
    final k = e.logicalKey;
    final toggle = _isToggleSidebar(k);
    if (!isDesktop ||
        !_isMac ||
        !liveTerminalHasFocus() ||
        !(_modOnly() || toggle)) {
      return KeyEventResult.ignored;
    }
    final result = _run(
      e,
      toggle
          ? widget.onToggleSidebar
          : k == LogicalKeyboardKey.keyK
          ? null
          : _modAction(k),
    );
    if (result == KeyEventResult.handled) _earlyDown.add(e.physicalKey);
    return result;
  }

  @override
  Widget build(BuildContext context) {
    if (!isDesktop) return widget.child;
    return Focus(autofocus: true, onKeyEvent: _onKey, child: widget.child);
  }
}
