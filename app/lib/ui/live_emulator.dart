import 'dart:convert';
import 'dart:typed_data';

import 'package:flterm/flterm.dart' as gt;
import 'package:flutter/gestures.dart' show kTouchSlop;
import 'package:flutter/material.dart';
import 'package:xterm/xterm.dart';

import '../state/pty_keys.dart';
import '../state/terminal_prefs.dart';
import '../state/touch_drag_filter.dart';
import '../util/utf8_stream.dart';
import 'patched_terminal.dart';
import 'theme.dart';

// Bundled so terminal glyphs match a desktop terminal regardless of device fonts.
const _fontFamily = monoFamily;
// NotoSansSymbols2 first so media-control symbols (⏺ ⏵ ⏸ …) render as text, not
// color emoji; emoji last as a fallback. The rest cover CJK.
const _fontFamilyFallback = <String>[
  'NotoSansSymbols2',
  'Noto Sans Mono CJK SC',
  'Noto Sans Mono CJK TC',
  'Noto Sans Mono CJK KR',
  'Noto Sans Mono CJK JP',
  'Noto Sans Mono CJK HK',
  'monospace',
  'Noto Color Emoji',
  'sans-serif',
];
const _padding = 8.0;

// A new TerminalTheme clears xterm's paint cache, so each tokens object
// gets one theme, reused across builds.
final _xtermThemes = Expando<TerminalTheme>();

TerminalTheme _xtermThemeOf(ArgusTokens t) =>
    _xtermThemes[t] ??= xtermThemeFrom(t);

TerminalTheme xtermThemeFrom(ArgusTokens t) => TerminalTheme(
      // xterm paints the block cursor over its cell; a translucent one keeps
      // the character under it readable.
      cursor: t.terminalCursor,
      selection: t.terminalSelection,
      foreground: t.terminalFg,
      background: t.terminalBg,
      black: t.ansi[0],
      red: t.ansi[1],
      green: t.ansi[2],
      yellow: t.ansi[3],
      blue: t.ansi[4],
      magenta: t.ansi[5],
      cyan: t.ansi[6],
      white: t.ansi[7],
      brightBlack: t.ansi[8],
      brightRed: t.ansi[9],
      brightGreen: t.ansi[10],
      brightYellow: t.ansi[11],
      brightBlue: t.ansi[12],
      brightMagenta: t.ansi[13],
      brightCyan: t.ansi[14],
      brightWhite: t.ansi[15],
      searchHitBackground: t.ansi[3],
      searchHitBackgroundCurrent: t.ansi[11],
      searchHitForeground: t.ansi[0],
    );

gt.TerminalTheme ghosttyThemeFrom(ArgusTokens t, {required double fontSize}) =>
    gt.TerminalTheme(
      palette: gt.ColorPalette(
        ansiColors: t.ansi,
        background: t.terminalBg,
        foreground: t.terminalFg,
      ),
      fontFamily: _fontFamily,
      fontFamilyFallback: _fontFamilyFallback,
      fontSize: fontSize,
    );

/// What an emulator needs from its live screen.
class LiveSink {
  const LiveSink({
    required this.send,
    required this.resize,
    required this.typed,
    required this.pinching,
  });

  final void Function(List<int> bytes) send;
  final void Function(int cols, int rows) resize;

  /// Keyboard text as bytes, with the key bar's armed modifiers applied.
  final List<int> Function(String data) typed;

  /// Whether a pinch is under way; mouse input pauses meanwhile.
  final bool Function() pinching;
}

/// Draws a live screen and encodes its input. With [direct], the keyboard
/// types into the terminal; otherwise only mouse input and the key bar leave
/// it.
abstract class LiveEmulator {
  factory LiveEmulator(TerminalEmulator kind,
          {required bool direct, required LiveSink sink}) =>
      switch (kind) {
        TerminalEmulator.xterm => _XtermEmulator(direct: direct, sink: sink),
        TerminalEmulator.ghostty => _GhosttyEmulator(direct: direct, sink: sink),
      };

  int get cols;
  int get rows;

  /// Forgets partial input from a previous attach.
  void reset();

  void write(List<int> bytes);

  /// Sends key bar key [name] (a [ptyKeyBytes] name).
  void pressKey(String name, {bool shift, bool alt, bool ctrl});

  Widget view(BuildContext context, double fontSize);

  void dispose();
}

class _XtermEmulator implements LiveEmulator {
  _XtermEmulator({required this.direct, required this.sink}) {
    _terminal.onResize = (w, h, pw, ph) => sink.resize(w, h);
    // TerminalView turns a vertical drag over the alt screen (tmux) into wheel
    // ticks, and taps into button presses. Send each as an SGR report straight
    // to the PTY, like the TUI.
    _terminal.mouseHandler = LiveScreenMouseHandler(
      onWheel: (up, pos) {
        if (sink.pinching()) return;
        sink.send(ptyWheelBytes(
            up, pos.x, pos.y, _terminal.viewWidth, _terminal.viewHeight));
      },
      onButton: (button, down, pos) {
        if (sink.pinching()) return;
        sink.send(ptyMouseBytes(button, down, pos.x, pos.y,
            _terminal.viewWidth, _terminal.viewHeight));
      },
    );
    if (direct) _terminal.onOutput = (data) => sink.send(sink.typed(data));
  }

  final bool direct;
  final LiveSink sink;
  final Terminal _terminal = PatchedTerminal(maxLines: 4000);
  // Reassembles UTF-8 codepoints split across output chunks.
  Utf8StreamDecoder _decoder = Utf8StreamDecoder();

  @override
  int get cols => _terminal.viewWidth;
  @override
  int get rows => _terminal.viewHeight;

  @override
  void reset() => _decoder = Utf8StreamDecoder();

  @override
  void write(List<int> bytes) => _terminal.write(_decoder.add(bytes));

  @override
  void pressKey(String name,
          {bool shift = false, bool alt = false, bool ctrl = false}) =>
      // Read per press so cursor keys follow the remote terminal's
      // application-cursor-key (DECCKM) state.
      sink.send(ptyKeyBytes(name,
          shift: shift, alt: alt, ctrl: ctrl, appCursor: _terminal.cursorKeysMode));

  @override
  Widget view(BuildContext context, double fontSize) => TerminalView(
        _terminal,
        theme: _xtermThemeOf(context.tokens),
        textStyle: TerminalStyle(
          fontSize: fontSize,
          fontFamily: _fontFamily,
          fontFamilyFallback: _fontFamilyFallback,
        ),
        padding: const EdgeInsets.all(_padding),
        readOnly: !direct,
        deleteDetection: direct,
        // Never turn a wheel tick into arrow keys: the remote program decides
        // what the wheel does.
        simulateScroll: false,
      );

  @override
  void dispose() {}
}

const _ghosttyKeys = <String, gt.Key>{
  'escape': gt.Key.escape,
  'tab': gt.Key.tab,
  'home': gt.Key.home,
  'end': gt.Key.end,
  'pgup': gt.Key.pageUp,
  'pgdown': gt.Key.pageDown,
  'up': gt.Key.arrowUp,
  'down': gt.Key.arrowDown,
  'left': gt.Key.arrowLeft,
  'right': gt.Key.arrowRight,
  'enter': gt.Key.enter,
  'backspace': gt.Key.backspace,
  'delete': gt.Key.delete,
};

class _GhosttyEmulator implements LiveEmulator {
  _GhosttyEmulator({required this.direct, required this.sink}) {
    _controller.onOutput = _onOutput;
    _controller.onResize = (cols, rows) {
      _cols = cols;
      _rows = rows;
      sink.resize(cols, rows);
    };
  }

  final bool direct;
  final LiveSink sink;
  final gt.TerminalController _controller = gt.TerminalController();
  final _focusNode = FocusNode();
  final _dragFilter = TouchDragFilter();
  ({Offset at, bool focused})? _tap;
  int _cols = 80, _rows = 24;
  // Set while a key bar key is encoded: sendKey answers through onOutput.
  var _fromKeyBar = false;

  void _onOutput(Uint8List raw) {
    if (_fromKeyBar) {
      sink.send(raw);
      return;
    }
    final out = direct ? _dragFilter.filter(raw) : _dragFilter.mouseOnly(raw);
    if (out.isEmpty) return;
    sink.send(direct ? sink.typed(utf8.decode(out, allowMalformed: true)) : out);
  }

  @override
  int get cols => _cols;
  @override
  int get rows => _rows;

  @override
  void reset() {}

  @override
  void write(List<int> bytes) => _controller.write(Uint8List.fromList(bytes));

  @override
  void pressKey(String name,
      {bool shift = false, bool alt = false, bool ctrl = false}) {
    final key = _ghosttyKeys[name];
    if (key == null) {
      sink.send(ptyKeyBytes(name, shift: shift, alt: alt, ctrl: ctrl));
      return;
    }
    var mods = const gt.Mods.none();
    if (shift) mods = mods | const gt.Mods.shift();
    if (alt) mods = mods | const gt.Mods.alt();
    if (ctrl) mods = mods | const gt.Mods.ctrl();
    _fromKeyBar = true;
    try {
      _controller.sendKey(key, mods: mods);
    } finally {
      _fromKeyBar = false;
    }
  }

  // flterm shows the keyboard only on focus gain, and believes it is still up
  // after the user hides it.
  void _onTapUp(BuildContext context, PointerUpEvent e) {
    final tap = _tap;
    _tap = null;
    if (tap == null || !tap.focused) return;
    if ((e.position - tap.at).distance > kTouchSlop) return;
    if (View.of(context).viewInsets.bottom > 0) return;
    _controller
      ..hideKeyboard()
      ..showKeyboard();
  }

  @override
  Widget view(BuildContext context, double fontSize) {
    final terminal = gt.TerminalView(
      controller: _controller,
      focusNode: _focusNode,
      showKeyboard: direct,
      theme: ghosttyThemeFrom(context.tokens, fontSize: fontSize),
    );
    if (!direct) return terminal;
    return Builder(
      builder: (context) => Listener(
        onPointerDown: (e) => _tap = _tap == null
            ? (at: e.position, focused: _focusNode.hasFocus)
            : null,
        onPointerUp: (e) => _onTapUp(context, e),
        onPointerCancel: (_) => _tap = null,
        child: terminal,
      ),
    );
  }

  @override
  void dispose() {
    _controller.dispose();
    _focusNode.dispose();
  }
}

/// Hands wheel ticks and button presses and releases to the live screen; the
/// emulator itself sends nothing.
class LiveScreenMouseHandler implements TerminalMouseHandler {
  const LiveScreenMouseHandler({required this.onWheel, required this.onButton});

  final void Function(bool up, CellOffset position) onWheel;
  final void Function(int button, bool down, CellOffset position) onButton;

  @override
  String? call(TerminalMouseEvent event) {
    final down = event.buttonState == TerminalMouseButtonState.down;
    switch (event.button) {
      case TerminalMouseButton.wheelUp when down:
        onWheel(true, event.position);
      case TerminalMouseButton.wheelDown when down:
        onWheel(false, event.position);
      case TerminalMouseButton.left ||
            TerminalMouseButton.middle ||
            TerminalMouseButton.right:
        onButton(event.button.id, down, event.position);
      default:
    }
    return null;
  }
}
