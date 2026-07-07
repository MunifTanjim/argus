import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:xterm/xterm.dart';

import '../data/terminal_repository.dart';
import '../models/enums.dart';
import '../models/session.dart';
import '../state/gateway.dart';
import '../state/pty_keys.dart';
import '../state/terminal_controller.dart';
import '../transport/connection.dart';
import '../util/utf8_stream.dart';
import 'ansi_palette.dart';

const _baseFontSize = 12.0;
const _minFontSize = 6.0;
const _maxFontSize = 40.0;
const _fontFamily = 'monospace';
const _padding = 8.0;

const _terminalTheme = TerminalTheme(
  cursor: ansiBrightBlack,
  selection: Color(0x40FFFFFF),
  foreground: Color(0xFFCCCCCC),
  background: ansiBlack,
  black: ansiBlack,
  red: ansiRed,
  green: ansiGreen,
  yellow: ansiYellow,
  blue: ansiBlue,
  magenta: ansiMagenta,
  cyan: ansiCyan,
  white: ansiWhite,
  brightBlack: ansiBrightBlack,
  brightRed: ansiBrightRed,
  brightGreen: ansiBrightGreen,
  brightYellow: ansiBrightYellow,
  brightBlue: ansiBrightBlue,
  brightMagenta: ansiBrightMagenta,
  brightCyan: ansiBrightCyan,
  brightWhite: ansiBrightWhite,
  searchHitBackground: ansiYellow,
  searchHitBackgroundCurrent: ansiBrightYellow,
  searchHitForeground: ansiBlack,
);

class LiveScreenScreen extends ConsumerStatefulWidget {
  const LiveScreenScreen({super.key, required this.session});

  final Session session;

  @override
  ConsumerState<LiveScreenScreen> createState() => _LiveScreenScreenState();
}

class _LiveScreenScreenState extends ConsumerState<LiveScreenScreen> {
  final Terminal _terminal = Terminal(maxLines: 4000);
  final TextEditingController _textController = TextEditingController();
  TerminalSession? _attach;
  // Reassembles UTF-8 codepoints split across output chunks. Reset per attach so
  // a partial sequence from a dead attach can't corrupt the next one's first glyph.
  Utf8StreamDecoder _decoder = Utf8StreamDecoder();
  // Only TerminalView depends on the font size, so drive it through a notifier
  // and rebuild just that subtree on pinch — not the input bar every frame.
  final ValueNotifier<double> _fontSize = ValueNotifier(_baseFontSize);

  // Manual pinch tracking via Listener so it never competes with the terminal
  // for the gesture arena. Zoom adjusts font size (crisp reflow); a smaller font
  // means more cols/rows, which TerminalView forwards to the PTY via onResize.
  final Map<int, Offset> _pointers = {};
  double? _pinchStartDist;
  double _pinchStartFont = _baseFontSize;

  void _onPointerDown(PointerDownEvent e) {
    _pointers[e.pointer] = e.position;
    if (_pointers.length == 2) {
      _pinchStartDist = _pointerDistance();
      _pinchStartFont = _fontSize.value;
    }
  }

  void _onPointerMove(PointerMoveEvent e) {
    if (!_pointers.containsKey(e.pointer)) return;
    _pointers[e.pointer] = e.position;
    final start = _pinchStartDist;
    if (_pointers.length == 2 && start != null && start > 0) {
      _fontSize.value = (_pinchStartFont * _pointerDistance() / start)
          .clamp(_minFontSize, _maxFontSize);
    }
  }

  void _onPointerUp(PointerEvent e) {
    _pointers.remove(e.pointer);
    if (_pointers.length < 2) _pinchStartDist = null;
  }

  double _pointerDistance() {
    final p = _pointers.values.toList();
    return (p[0] - p[1]).distance;
  }

  @override
  void initState() {
    super.initState();
    // Forward viewport resizes to the node so the remote PTY tracks the screen.
    _terminal.onResize = (w, h, pw, ph) => _attach?.resize(w, h);
    WidgetsBinding.instance.addPostFrameCallback((_) => _open());
  }

  void _open() {
    // Post-frame callbacks fire even if the element was disposed before the first
    // frame (fast navigation away). Bail so we don't read a disposed Ref or leave
    // a live attach that dispose() already ran past.
    if (!mounted) return;
    _attach?.dispose();
    _decoder = Utf8StreamDecoder();
    _attach = ref.read(terminalRepositoryProvider).open(
          sessionId: widget.session.id,
          cols: _terminal.viewWidth,
          rows: _terminal.viewHeight,
          onData: (bytes) {
            if (mounted) _terminal.write(_decoder.add(bytes));
          },
          onExited: _onExited,
          onError: (e) {
            // Open failed: don't strand the user on a dead black screen. Surface
            // the error and leave (the attach self-disposes its subscription).
            if (!mounted) return;
            ScaffoldMessenger.of(context)
                .showSnackBar(SnackBar(content: Text('attach failed: $e')));
            Navigator.of(context).maybePop();
          },
        );
    // No client yet (not connected): open() returns null, so there's nothing to
    // stream. Tell the user instead of leaving a blank black terminal.
    if (_attach == null && mounted) {
      ScaffoldMessenger.of(context)
          .showSnackBar(const SnackBar(content: Text('not connected')));
    }
  }

  // Attach ended node-side: leave instead of showing a frozen screen. An evicted
  // attach was booted because the session was opened elsewhere (last opener wins).
  void _onExited(TerminalExitReason reason) {
    if (!mounted) return;
    final message = reason == TerminalExitReason.evicted
        ? 'terminal opened elsewhere'
        : 'terminal exited';
    ScaffoldMessenger.of(context)
        .showSnackBar(SnackBar(content: Text(message)));
    Navigator.of(context).maybePop();
  }

  @override
  void dispose() {
    _attach?.dispose();
    _textController.dispose();
    _fontSize.dispose();
    super.dispose();
  }

  void _send(List<int> bytes) => _attach?.send(bytes);

  @override
  Widget build(BuildContext context) {
    // Match the TUI: leave the live screen on disconnect rather than silently
    // re-attaching. The gateway-side term is dead once the connection drops, so
    // the user re-enters the screen (minting a fresh attach) after reconnect.
    ref.listen<ConnState>(connStateProvider, (prev, next) {
      if (prev == ConnState.connected && next != ConnState.connected && mounted) {
        ScaffoldMessenger.of(context)
            .showSnackBar(const SnackBar(content: Text('terminal detached')));
        Navigator.of(context).maybePop();
      }
    });

    final title = widget.session.displayTitle;
    return Scaffold(
      appBar: AppBar(title: Text(title)),
      backgroundColor: Colors.black,
      body: Column(
        children: [
          Expanded(
            child: Listener(
              onPointerDown: _onPointerDown,
              onPointerMove: _onPointerMove,
              onPointerUp: _onPointerUp,
              onPointerCancel: _onPointerUp,
              child: ValueListenableBuilder<double>(
                valueListenable: _fontSize,
                builder: (context, fontSize, _) => TerminalView(
                  _terminal,
                  theme: _terminalTheme,
                  textStyle:
                      TerminalStyle(fontSize: fontSize, fontFamily: _fontFamily),
                  padding: const EdgeInsets.all(_padding),
                  // Input goes through _InputBar (raw PTY bytes), not the grid.
                  readOnly: true,
                ),
              ),
            ),
          ),
          _InputBar(
            controller: _textController,
            onInput: _send,
            // Read at press time so cursor keys follow the remote terminal's
            // application-cursor-key (DECCKM) state.
            appCursorMode: () => _terminal.cursorKeysMode,
          ),
        ],
      ),
    );
  }
}

class _InputBar extends StatefulWidget {
  const _InputBar({
    required this.controller,
    required this.onInput,
    required this.appCursorMode,
  });

  final TextEditingController controller;
  final void Function(List<int> bytes) onInput;

  /// Whether the remote terminal is in application-cursor-key mode (DECCKM),
  /// evaluated per keypress so cursor keys emit the right escape sequence.
  final bool Function() appCursorMode;

  @override
  State<_InputBar> createState() => _InputBarState();
}

class _InputBarState extends State<_InputBar> {
  // One-shot modifiers: applied to the next char, then auto-cleared.
  bool _ctrl = false;
  bool _alt = false;
  bool _shift = false;

  bool get _hasMod => _ctrl || _alt || _shift;

  // Only Ctrl/Alt modify a typed character; Shift shapes keycaps (Tab/Enter), not
  // text. So armed Shift must not trigger the send-on-type path below.
  bool get _hasCharMod => _ctrl || _alt;

  void _clearMods() {
    if (_hasMod) setState(() => _ctrl = _alt = _shift = false);
  }

  void _pressKey(String name) {
    widget.onInput(ptyKeyBytes(name,
        shift: _shift, alt: _alt, appCursor: widget.appCursorMode()));
    _clearMods();
  }

  void _pressChar(String ch) {
    widget.onInput(ptyTextBytes(ch, ctrl: _ctrl, alt: _alt));
    widget.controller.clear();
    _clearMods();
  }

  void _sendText() {
    final text = widget.controller.text;
    if (text.isEmpty) return;
    widget.onInput(ptyTextBytes(text));
    widget.controller.clear();
  }

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              // Keys with a clear glyph use an icon; the rest fall back to a
              // short text label. Modifiers pass `active:` to show armed state.
              children: [
                _keyRow([
                  _capButton(_label('Esc'), 'Escape', () => _pressKey('escape')),
                  _capButton(_icon(Icons.keyboard_tab), 'Tab',
                      () => _pressKey('tab')),
                  _capButton(_label('Home'), 'Home', () => _pressKey('home')),
                  _capButton(_label('End'), 'End', () => _pressKey('end')),
                  _capButton(_label('PgUp'), 'PageUp', () => _pressKey('pgup')),
                  _capButton(_icon(Icons.keyboard_arrow_up), 'Up',
                      () => _pressKey('up')),
                  _capButton(_label('PgDn'), 'PageDown',
                      () => _pressKey('pgdown')),
                  _capButton(_icon(Icons.backspace_outlined), 'Backspace',
                      () => _pressKey('backspace')),
                ]),
                const SizedBox(height: 4),
                _keyRow([
                  _capButton(_label('Del'), 'Delete', () => _pressKey('delete')),
                  _capButton(_icon(Icons.keyboard_capslock), 'Shift',
                      () => setState(() => _shift = !_shift), active: _shift),
                  _capButton(_icon(Icons.keyboard_control_key), 'Ctrl',
                      () => setState(() => _ctrl = !_ctrl), active: _ctrl),
                  _capButton(_icon(Icons.keyboard_option_key), 'Alt',
                      () => setState(() => _alt = !_alt), active: _alt),
                  _capButton(_icon(Icons.keyboard_arrow_left), 'Left',
                      () => _pressKey('left')),
                  _capButton(_icon(Icons.keyboard_arrow_down), 'Down',
                      () => _pressKey('down')),
                  _capButton(_icon(Icons.keyboard_arrow_right), 'Right',
                      () => _pressKey('right')),
                  _capButton(_icon(Icons.keyboard_return), 'Enter',
                      () => _pressKey('enter')),
                ]),
              ],
            ),
          ),
          // Text input row. While a modifier is armed, the next typed character
          // is sent as a modified key instead of buffered.
          Padding(
            padding: const EdgeInsets.fromLTRB(8, 0, 8, 8),
            child: Row(
              children: [
                Expanded(
                  child: TextField(
                    controller: widget.controller,
                    style: const TextStyle(fontFamily: 'monospace'),
                    onChanged: (v) {
                      // With Ctrl/Alt armed, apply it to the last rune (not a
                      // substring, so an emoji isn't split into a lone surrogate).
                      // Shift is excluded above: it can't modify a character.
                      if (_hasCharMod && v.isNotEmpty) {
                        _pressChar(String.fromCharCode(v.runes.last));
                      }
                    },
                    onSubmitted: (_) => _sendText(),
                    decoration: const InputDecoration(
                      isDense: true,
                      contentPadding:
                          EdgeInsets.symmetric(horizontal: 8, vertical: 8),
                      border: OutlineInputBorder(),
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                ElevatedButton(onPressed: _sendText, child: const Text('Send')),
              ],
            ),
          ),
        ],
      ),
    );
  }

  // A full-width row of equal-width keycaps — no horizontal scroll.
  Widget _keyRow(List<Widget> keys) => Row(
        children: keys
            .map((b) => Expanded(
                  child: Padding(
                    padding: const EdgeInsets.symmetric(horizontal: 2),
                    child: b,
                  ),
                ))
            .toList(),
      );

  // Compact terminal-like keycaps.
  static const _keycapFill = Color(0xFF2A2A2A);
  static const _keycapBorder = Color(0xFF444444);
  static const _keycapText = Color(0xFFDDDDDD);
  static const _armedFill = Color(0xFF3B82F6);
  static const _btnStyle = TextStyle(fontFamily: 'monospace', fontSize: 11);

  ButtonStyle _capStyle({required bool active}) => OutlinedButton.styleFrom(
        padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
        minimumSize: const Size(0, 28),
        tapTargetSize: MaterialTapTargetSize.shrinkWrap,
        backgroundColor: active ? _armedFill : _keycapFill,
        foregroundColor: active ? Colors.white : _keycapText,
        side: BorderSide(color: active ? _armedFill : _keycapBorder),
        shape: const RoundedRectangleBorder(
            borderRadius: BorderRadius.all(Radius.circular(6))),
      );

  // Icon color/size inherit from the button's foreground (active vs idle).
  Widget _icon(IconData d) => Icon(d, size: 16);
  Widget _label(String s) => Text(s, style: _btnStyle);

  // One keycap. Modifiers pass active: true to render the armed highlight.
  Widget _capButton(Widget child, String tooltip, VoidCallback onPressed,
          {bool active = false}) =>
      Tooltip(
        message: tooltip,
        preferBelow: false,
        child: OutlinedButton(
          style: _capStyle(active: active),
          onPressed: onPressed,
          child: child,
        ),
      );
}
