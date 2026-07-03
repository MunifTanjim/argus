import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:xterm/xterm.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../models/session.dart';
import '../state/live_screen_view_model.dart';
import 'ansi_palette.dart';

// Full-reset prefix written before each snapshot: clear scrollback, clear
// screen, home the cursor. capture-pane gives a positioned full-screen dump,
// so we overwrite in place rather than stream.
const _clearScreen = '\x1b[3J\x1b[2J\x1b[H';

// tmux capture uses bare LF line separators; a terminal needs CR+LF to avoid a
// staircase. Normalise any CRLF first so we don't double the CR.
String _crlf(String s) =>
    s.replaceAll('\r\n', '\n').replaceAll('\n', '\r\n');

const _baseFontSize = 12.0;
const _minFontSize = 6.0;
const _maxFontSize = 40.0;
const _fontFamily = 'monospace';
const _padding = 8.0;

// CSI / OSC / lone-ESC sequences, stripped when measuring visible line width.
final _ansiEscape = RegExp(
    r'\x1B\[[0-9;?]*[ -/]*[@-~]|\x1B\][^\x07\x1B]*(?:\x07|\x1B\\)|\x1B.');

// Widest visible line in columns, so the terminal grid can be sized to fit
// without wrapping. Runes approximate cells (wide chars counted as one).
int _measureCols(String screen) {
  var max = 0;
  for (final line in screen.split('\n')) {
    final n = line.replaceAll('\r', '').replaceAll(_ansiEscape, '').runes.length;
    if (n > max) max = n;
  }
  return max;
}

// Advance width of one monospace cell at [fontSize].
double _cellWidth(double fontSize) {
  final tp = TextPainter(
    text: TextSpan(
      text: 'WWWWWWWWWW',
      style: TextStyle(fontFamily: _fontFamily, fontSize: fontSize),
    ),
    textDirection: TextDirection.ltr,
  )..layout();
  return tp.width / 10;
}

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
  late final LiveScreenViewModel _vm;
  final Terminal _terminal = Terminal(maxLines: 4000);
  int _cols = 80;
  double _fontSize = _baseFontSize;
  Timer? _timer;
  final TextEditingController _textController = TextEditingController();

  // Manual pinch tracking via Listener so it never competes with the scroll
  // views for the gesture arena. Zoom adjusts font size (crisp reflow), not a
  // bitmap transform.
  final Map<int, Offset> _pointers = {};
  double? _pinchStartDist;
  double _pinchStartFont = _baseFontSize;

  void _onPointerDown(PointerDownEvent e) {
    _pointers[e.pointer] = e.position;
    if (_pointers.length == 2) {
      _pinchStartDist = _pointerDistance();
      _pinchStartFont = _fontSize;
    }
  }

  void _onPointerMove(PointerMoveEvent e) {
    if (!_pointers.containsKey(e.pointer)) return;
    _pointers[e.pointer] = e.position;
    final start = _pinchStartDist;
    if (_pointers.length == 2 && start != null && start > 0) {
      final f = (_pinchStartFont * _pointerDistance() / start)
          .clamp(_minFontSize, _maxFontSize);
      if (f != _fontSize) setState(() => _fontSize = f);
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
    _vm = LiveScreenViewModel(
        ref.read(sessionRepositoryProvider), widget.session.id);
    _vm.capture.addListener(_onCapture);
    // Immediate capture on init.
    _vm.capture.execute();
    // Periodic polling every 750 ms; the command's running-guard drops ticks
    // that would overlap an in-flight capture.
    _timer = Timer.periodic(const Duration(milliseconds: 750), (_) {
      _vm.capture.execute();
    });
  }

  void _onCapture() {
    if (!mounted) return;
    // A failed capture is a transient gap (reconnect / dead pane); the next
    // tick retries, so errors are intentionally dropped here.
    if (_vm.capture.result case Ok(:final value)) {
      _terminal.write(_clearScreen);
      _terminal.write(_crlf(value));
      final cols = _measureCols(value);
      if (cols != _cols) setState(() => _cols = cols);
    }
  }

  @override
  void dispose() {
    _timer?.cancel();
    _vm.capture.removeListener(_onCapture);
    _vm.dispose();
    _textController.dispose();
    super.dispose();
  }

  void _report(Result<void>? result) {
    if (!mounted) return;
    if (result case Error(:final error)) {
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text('Failed: $error')));
    }
  }

  Future<void> _sendKeys(List<String> keys) async {
    await _vm.sendKeys.execute(keys);
    _report(_vm.sendKeys.result);
  }

  Future<void> _sendRaw(String text) async {
    _textController.clear();
    await _vm.sendRaw.execute(text);
    _report(_vm.sendRaw.result);
  }

  @override
  Widget build(BuildContext context) {
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
              child: LayoutBuilder(
                builder: (context, constraints) {
                  // Size the grid to the widest line so nothing wraps; wider-than-
                  // viewport content scrolls horizontally. +1 col of slack absorbs
                  // cell-metric rounding.
                  final contentW =
                      (_cols + 1) * _cellWidth(_fontSize) + _padding * 2;
                  final width = contentW < constraints.maxWidth
                      ? constraints.maxWidth
                      : contentW;
                  return SingleChildScrollView(
                    scrollDirection: Axis.horizontal,
                    child: SizedBox(
                      width: width,
                      height: constraints.maxHeight,
                      child: TerminalView(
                        _terminal,
                        theme: _terminalTheme,
                        textStyle: TerminalStyle(
                            fontSize: _fontSize, fontFamily: _fontFamily),
                        padding: const EdgeInsets.all(_padding),
                        // Input goes through _InputBar / sendKeys, not the grid.
                        readOnly: true,
                      ),
                    ),
                  );
                },
              ),
            ),
          ),
          _InputBar(
            controller: _textController,
            onSend: _sendRaw,
            onKey: _sendKeys,
          ),
        ],
      ),
    );
  }
}

class _InputBar extends StatefulWidget {
  const _InputBar({
    required this.controller,
    required this.onSend,
    required this.onKey,
  });

  final TextEditingController controller;
  final Future<void> Function(String text) onSend;
  final Future<void> Function(List<String> keys) onKey;

  @override
  State<_InputBar> createState() => _InputBarState();
}

class _InputBarState extends State<_InputBar> {
  // One-shot modifiers: applied to the next key/char, then auto-cleared.
  bool _ctrl = false;
  bool _alt = false;
  bool _shift = false;

  bool get _hasMod => _ctrl || _alt || _shift;

  void _clearMods() {
    if (_hasMod) setState(() => _ctrl = _alt = _shift = false);
  }

  // tmux modifier prefix; combinable, e.g. C-S-Up.
  String _prefix({bool shift = true}) =>
      '${_ctrl ? 'C-' : ''}${_alt ? 'M-' : ''}${shift && _shift ? 'S-' : ''}';

  void _pressKey(String base) {
    // tmux spells shift+Tab as BTab, not S-Tab.
    if (_shift && base == 'Tab') {
      widget.onKey(['${_prefix(shift: false)}BTab']);
    } else {
      widget.onKey(['${_prefix()}$base']);
    }
    _clearMods();
  }

  // Send a single typed character with the active modifiers (e.g. Ctrl+c).
  void _pressChar(String ch) {
    // Ctrl/Alt combos expect a bare letter; shift picks the case.
    if ((_ctrl || _alt) && ch.length == 1) {
      ch = _shift ? ch.toUpperCase() : ch.toLowerCase();
    }
    widget.onKey(['${_prefix()}$ch']);
    widget.controller.clear();
    _clearMods();
  }

  @override
  Widget build(BuildContext context) {
    return SafeArea(
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Padding(
            padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 4),
            child: LayoutBuilder(
              builder: (context, constraints) {
                final keys = <Widget>[
                _keyButton(
                    _label('Esc'), 'Escape', () => _pressKey('Escape')),
                _keyButton(
                    _icon(Icons.keyboard_tab), 'Tab', () => _pressKey('Tab')),
                _modButton(_icon(Icons.keyboard_capslock), 'Shift', _shift,
                    () => setState(() => _shift = !_shift)),
                _modButton(_icon(Icons.keyboard_control_key), 'Ctrl', _ctrl,
                    () => setState(() => _ctrl = !_ctrl)),
                _modButton(_icon(Icons.keyboard_option_key), 'Alt', _alt,
                    () => setState(() => _alt = !_alt)),
                _keyButton(_icon(Icons.keyboard_return), 'Enter',
                    () => _pressKey('Enter')),
                _keyButton(_icon(Icons.keyboard_arrow_left), 'Left',
                    () => _pressKey('Left')),
                _keyButton(_icon(Icons.keyboard_arrow_down), 'Down',
                    () => _pressKey('Down')),
                _keyButton(_icon(Icons.keyboard_arrow_up), 'Up',
                    () => _pressKey('Up')),
                _keyButton(_icon(Icons.keyboard_arrow_right), 'Right',
                    () => _pressKey('Right')),
                _keyButton(_icon(Icons.backspace_outlined), 'Backspace',
                    () => _pressKey('BSpace')),
                ];
                // Fill the width when keys fit; otherwise clamp to a min tap
                // size and scroll.
                final w = (constraints.maxWidth / keys.length)
                    .clamp(36.0, double.infinity);
                return SingleChildScrollView(
                  scrollDirection: Axis.horizontal,
                  child: Row(
                    children: keys
                        .map((b) => SizedBox(
                              width: w,
                              child: Padding(
                                padding:
                                    const EdgeInsets.symmetric(horizontal: 2),
                                child: b,
                              ),
                            ))
                        .toList(),
                  ),
                );
              },
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
                      if (_hasMod && v.isNotEmpty) {
                        _pressChar(v.substring(v.length - 1));
                      }
                    },
                    decoration: const InputDecoration(
                      isDense: true,
                      contentPadding:
                          EdgeInsets.symmetric(horizontal: 8, vertical: 8),
                      border: OutlineInputBorder(),
                    ),
                  ),
                ),
                const SizedBox(width: 8),
                ElevatedButton(
                  onPressed: () {
                    final text = widget.controller.text;
                    if (text.isNotEmpty) widget.onSend(text);
                  },
                  child: const Text('Send'),
                ),
              ],
            ),
          ),
        ],
      ),
    );
  }

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
        shape:
            const RoundedRectangleBorder(borderRadius: BorderRadius.all(Radius.circular(6))),
      );

  // Icon color/size inherit from the button's foreground (active vs idle).
  Widget _icon(IconData d) => Icon(d, size: 16);
  Widget _label(String s) => Text(s, style: _btnStyle);

  Widget _keyButton(Widget child, String tooltip, VoidCallback onPressed) =>
      Tooltip(
        message: tooltip,
        preferBelow: false,
        child: OutlinedButton(
          style: _capStyle(active: false),
          onPressed: onPressed,
          child: child,
        ),
      );

  Widget _modButton(
          Widget child, String tooltip, bool active, VoidCallback onTap) =>
      Tooltip(
        message: tooltip,
        preferBelow: false,
        child: OutlinedButton(
          style: _capStyle(active: active),
          onPressed: onTap,
          child: child,
        ),
      );
}
