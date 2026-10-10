import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/terminal_repository.dart';
import '../models/enums.dart';
import '../models/session.dart';
import '../models/terminal.dart';
import '../state/gateway.dart';
import '../state/input_batcher.dart';
import '../state/pty_keys.dart';
import '../state/terminal_controller.dart';
import '../state/terminal_prefs.dart';
import '../state/terminals.dart';
import '../transport/connection.dart';
import 'live_emulator.dart';


class LiveScreenScreen extends ConsumerStatefulWidget {
  const LiveScreenScreen({super.key, this.session, this.terminal})
      : assert((session == null) != (terminal == null));

  final Session? session;
  final NodeTerminal? terminal;

  @override
  ConsumerState<LiveScreenScreen> createState() => _LiveScreenScreenState();
}

class _LiveScreenScreenState extends ConsumerState<LiveScreenScreen> {
  final TextEditingController _textController = TextEditingController();
  TerminalSession? _attach;
  // A terminal takes keys straight from the keyboard; they and the key bar go
  // out in batches. A session keeps the text box and sends each input at once.
  late final InputBatcher _batcher = InputBatcher((b) => _attach?.send(b));
  final _inputBar = GlobalKey<_InputBarState>();
  bool get _direct => widget.terminal != null;
  late final LiveEmulator _emulator = LiveEmulator(
    ref.read(terminalPrefsProvider).emulator,
    direct: _direct,
    sink: LiveSink(
      send: _send,
      resize: (cols, rows) => _attach?.resize(cols, rows),
      typed: (data) => _inputBar.currentState?.typed(data) ?? utf8.encode(data),
      pinching: () => _pinchStartDist != null,
    ),
  );
  // Only TerminalView depends on the font size, so drive it through a notifier
  // and rebuild just that subtree on pinch — not the input bar every frame.
  late final ValueNotifier<double> _fontSize =
      ValueNotifier(ref.read(terminalPrefsProvider).fontSize);

  // Manual pinch tracking via Listener so it never competes with the terminal
  // for the gesture arena. Zoom adjusts font size (crisp reflow); a smaller font
  // means more cols/rows, which TerminalView forwards to the PTY via onResize.
  final Map<int, Offset> _pointers = {};
  double? _pinchStartDist;
  double _pinchStartFont = terminalFontSizeDefault;

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
          .clamp(terminalFontSizeMin, terminalFontSizeMax);
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
    // Read the settings here, not lazily in build, where ref.read is not meant to run.
    _emulator;
    _fontSize;
    WidgetsBinding.instance.addPostFrameCallback((_) => _open());
  }

  void _open() {
    // Post-frame callbacks fire even if the element was disposed before the first
    // frame (fast navigation away). Bail so we don't read a disposed Ref or leave
    // a live attach that dispose() already ran past.
    if (!mounted) return;
    _attach?.dispose();
    // A partial UTF-8 sequence from a dead attach must not corrupt the next one.
    _emulator.reset();
    _attach = ref.read(terminalRepositoryProvider).open(
          sessionId: widget.session?.id,
          terminalId: widget.terminal?.id,
          cols: _emulator.cols,
          rows: _emulator.rows,
          onData: (bytes) {
            if (mounted) _emulator.write(bytes);
          },
          onExited: _onExited,
          onError: (e) {
            // Open failed: don't strand the user on a dead black screen. Surface
            // the error and leave (the attach self-disposes its subscription).
            if (!mounted) return;
            // A terminal can be gone with no terminal.changed (its shell exited
            // while no one watched), so the list reloads.
            if (widget.terminal != null) {
              unawaited(ref
                  .read(terminalsProvider.notifier)
                  .load(ref.read(gatewayProvider)?.client));
            }
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
    _batcher.flush();
    _attach?.dispose();
    _emulator.dispose();
    _textController.dispose();
    _fontSize.dispose();
    super.dispose();
  }

  void _send(List<int> bytes) =>
      _direct ? _batcher.add(bytes) : _attach?.send(bytes);

  Future<void> _switchTerminal(List<NodeTerminal> siblings) async {
    final current = widget.terminal!;
    final next = await showModalBottomSheet<NodeTerminal>(
      context: context,
      useRootNavigator: true,
      builder: (ctx) => SafeArea(
        child: ListView(
          shrinkWrap: true,
          children: [
            for (final t in siblings)
              ListTile(
                leading: const Icon(Icons.terminal),
                title: Text(t.title),
                subtitle: Text(t.cwd),
                selected: t.id == current.id,
                trailing: t.id == current.id ? const Icon(Icons.check) : null,
                onTap: () => Navigator.of(ctx).pop(t),
              ),
          ],
        ),
      ),
    );
    if (!mounted || next == null || next.id == current.id) return;
    unawaited(Navigator.of(context).pushReplacement(PageRouteBuilder(
      transitionDuration: Duration.zero,
      reverseTransitionDuration: Duration.zero,
      pageBuilder: (_, _, _) => LiveScreenScreen(terminal: next),
    )));
  }

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

    final t = widget.terminal;
    final title = t != null
        ? (t.nodeLabel.isEmpty ? t.title : '${t.title} · ${t.nodeLabel}')
        : widget.session!.displayTitle;
    final siblings = t == null || t.workspaceId.isEmpty
        ? const <NodeTerminal>[]
        : [
            for (final s in ref.watch(terminalsProvider).terminals)
              if (s.workspaceId == t.workspaceId) s,
          ];
    return Scaffold(
      appBar: AppBar(
        title: Text(title),
        actions: [
          if (siblings.length > 1)
            IconButton(
              icon: const Icon(Icons.swap_horiz),
              tooltip: 'Switch terminal',
              onPressed: () => _switchTerminal(siblings),
            ),
        ],
      ),
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
                builder: (context, fontSize, _) => _emulator.view(fontSize),
              ),
            ),
          ),
          _InputBar(
            key: _inputBar,
            compose: !_direct,
            controller: _textController,
            onInput: _send,
            onKey: _emulator.pressKey,
          ),
        ],
      ),
    );
  }
}


class _InputBar extends StatefulWidget {
  const _InputBar({
    super.key,
    required this.compose,
    required this.controller,
    required this.onInput,
    required this.onKey,
  });

  /// Whether to show the text box and Send button; without them the keyboard
  /// types into the terminal.
  final bool compose;
  final TextEditingController controller;
  final void Function(List<int> bytes) onInput;

  /// Sends a key bar key through the emulator, which encodes it for the
  /// remote terminal's current modes.
  final void Function(String name, {bool shift, bool alt, bool ctrl}) onKey;

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
    widget.onKey(name, shift: _shift, alt: _alt, ctrl: _ctrl);
    _clearMods();
  }

  void _pressChar(String ch) {
    widget.onInput(ptyTextBytes(ch, ctrl: _ctrl, alt: _alt));
    widget.controller.clear();
    _clearMods();
  }

  /// The bytes for [data] typed on the keyboard, with an armed Ctrl or Alt
  /// applied to a single character, which also disarms them.
  List<int> typed(String data) {
    if (!_hasCharMod || data.runes.length != 1) return utf8.encode(data);
    final out = ptyTextBytes(data, ctrl: _ctrl, alt: _alt);
    _clearMods();
    return out;
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
          if (widget.compose)
            Padding(
              padding: const EdgeInsets.fromLTRB(8, 0, 8, 8),
              child: Row(
                children: [
                  Expanded(
                    child: TextField(
                      controller: widget.controller,
                      style: const TextStyle(fontFamily: 'monospace'),
                      minLines: 1,
                      maxLines: 3,
                      onChanged: (v) {
                        // With Ctrl/Alt armed, apply it to the last rune (not a
                        // substring, so an emoji isn't split into a lone surrogate).
                        // Shift is excluded above: it can't modify a character.
                        if (_hasCharMod && v.isNotEmpty) {
                          _pressChar(String.fromCharCode(v.runes.last));
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
