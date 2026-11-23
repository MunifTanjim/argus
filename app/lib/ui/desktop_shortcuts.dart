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

/// Whether the primary focus is inside an [EditableText].
bool textInputHasFocus() {
  final ctx = primaryFocus?.context;
  if (ctx == null) return false;
  return ctx.widget is EditableText ||
      ctx.findAncestorWidgetOfExactType<EditableText>() != null;
}

bool liveTerminalHasFocus() => liveTerminalFocusNodes.any((n) => n.hasFocus);

bool get _isMac => defaultTargetPlatform == TargetPlatform.macOS;

/// The bindings, for the shortcut sheet. Labels use ⌘ on macOS, Ctrl on Linux.
List<({String keys, String action})> shortcutDescriptions() {
  String mod(String key) => _isMac ? '⌘$key' : 'Ctrl+$key';
  return [
    (keys: mod('K'), action: 'Quick switcher'),
    (keys: mod('N'), action: 'New session'),
    (keys: '${mod('1')} – ${mod('4')}', action: 'Switch tab'),
    (keys: mod('['), action: 'Back'),
    (keys: mod('L'), action: 'Focus the reply box'),
    (keys: mod(','), action: 'Settings'),
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
class DesktopShortcuts extends StatelessWidget {
  const DesktopShortcuts({
    super.key,
    required this.child,
    required this.onBack,
    required this.onSpawn,
    required this.onSettings,
    required this.onQuickSwitcher,
    required this.onShortcutSheet,
  });

  final Widget child;
  final VoidCallback onBack;
  final VoidCallback onSpawn;
  final VoidCallback onSettings;
  final VoidCallback onQuickSwitcher;
  final VoidCallback onShortcutSheet;

  // Mod shortcuts. On Linux, Ctrl keys belong to a focused terminal.
  bool get _guardA => _isMac || !liveTerminalHasFocus();

  // Single-key shortcuts never take a key from a text field or a terminal.
  bool get _guardB => !textInputHasFocus() && !liveTerminalHasFocus();

  static bool _popupOnTop() {
    final ctx = primaryFocus?.context;
    return ctx != null && ModalRoute.of(ctx) is PopupRoute;
  }

  void _focusComposer() => activeComposerFocus.value?.requestFocus();

  /// The action for [e], or null if no binding matches.
  VoidCallback? _match(KeyEvent e) {
    final kb = HardwareKeyboard.instance;
    final k = e.logicalKey;
    final meta = kb.isMetaPressed, ctrl = kb.isControlPressed;
    final alt = kb.isAltPressed, shift = kb.isShiftPressed;
    final mod = _isMac ? meta && !ctrl : ctrl && !meta;
    if (mod && !alt && !shift) {
      if (!_guardA) return null;
      if (k == LogicalKeyboardKey.keyK) return onQuickSwitcher;
      if (k == LogicalKeyboardKey.keyN) return onSpawn;
      if (k == LogicalKeyboardKey.bracketLeft) return onBack;
      if (k == LogicalKeyboardKey.keyL) return _focusComposer;
      if (k == LogicalKeyboardKey.comma) return onSettings;
      final tab = _tabKeys.indexOf(k);
      if (tab >= 0) return () => selectVisibleTab(tab);
      return null;
    }
    if (meta || ctrl || alt) return null;
    final question = k == LogicalKeyboardKey.question ||
        (shift && k == LogicalKeyboardKey.slash);
    if (question) return _guardB ? onShortcutSheet : null;
    if (shift || !_guardB) return null;
    if (k == LogicalKeyboardKey.slash) {
      return activeComposerFocus.value == null ? null : _focusComposer;
    }
    if (k == LogicalKeyboardKey.escape) return _popupOnTop() ? null : onBack;
    if (k == LogicalKeyboardKey.keyJ) {
      return () => FocusScope.of(primaryFocus!.context!).nextFocus();
    }
    if (k == LogicalKeyboardKey.keyK) {
      return () => FocusScope.of(primaryFocus!.context!).previousFocus();
    }
    return null;
  }

  KeyEventResult _onKey(FocusNode _, KeyEvent e) {
    if (e is KeyUpEvent) return KeyEventResult.ignored;
    final action = _match(e);
    if (action == null) return KeyEventResult.ignored;
    // A held key repeats: swallow the repeats without acting again.
    if (e is KeyDownEvent) action();
    return KeyEventResult.handled;
  }

  @override
  Widget build(BuildContext context) {
    if (!isDesktop) return child;
    return Focus(autofocus: true, onKeyEvent: _onKey, child: child);
  }
}
