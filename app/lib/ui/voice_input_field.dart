import 'dart:async';

import 'package:flutter/gestures.dart';
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import '../data/dictation_engine.dart';
import '../state/voice.dart';
import 'theme.dart';

/// Wraps the text field that [field] builds with dictation for [controller].
///
/// When voice input is on, [field] receives a placeholder to pass as its
/// `suffixIcon`. The placeholder keeps typed text clear of the mic, which sits
/// over the field's bottom-right corner. While recording or transcribing, a
/// status row with the clock, the level meter and cancel shows above the field.
/// When voice input is off, [field] receives null and is returned as is.
class VoiceInputField extends ConsumerWidget {
  const VoiceInputField({
    super.key,
    required this.controller,
    required this.field,
    this.onChanged,
    this.statusBelow = false,
  });

  final TextEditingController controller;
  final Widget Function(Widget? suffixIcon) field;

  /// Mirrors the field's own `onChanged`. Dictation bypasses the keyboard, so
  /// fields that track their text separately (drafts, validation) need telling.
  final ValueChanged<String>? onChanged;

  /// Shows the status row below the field instead of above it. Pick the side
  /// that keeps the field still when the row appears in the host's layout.
  final bool statusBelow;

  @override
  Widget build(BuildContext context, WidgetRef ref) =>
      ref.watch(voicePrefsProvider.select((p) => p.enabled))
          ? _Dictation(
              controller: controller,
              field: field,
              onChanged: onChanged,
              statusBelow: statusBelow,
            )
          : field(null);
}

enum _Phase { idle, recording, transcribing, message }

/// Dictates into the field at the cursor. A live engine updates the text while
/// the user speaks, and OpenRouter drops it in once transcribed. It never
/// sends: the user reads and edits the transcript, then presses the field's own
/// send button.
class _Dictation extends ConsumerStatefulWidget {
  const _Dictation({
    required this.controller,
    required this.field,
    required this.statusBelow,
    this.onChanged,
  });

  final TextEditingController controller;
  final Widget Function(Widget? suffixIcon) field;
  final ValueChanged<String>? onChanged;
  final bool statusBelow;

  @override
  ConsumerState<_Dictation> createState() => _DictationState();
}

class _DictationState extends ConsumerState<_Dictation> {
  /// Drives both the clock readout and the meter. 200ms is fast enough to look
  /// live and slow enough not to churn the widget tree.
  static const _tick = Duration(milliseconds: 200);

  /// Enough level history to fill the meter across a tablet-width row.
  static const _maxLevels = 200;

  /// Shorter holds are taken as taps and discarded, so a stray touch does not
  /// upload, and bill, a clip of silence.
  static const _minHold = Duration(seconds: 1);
  static const _cancelDistance = 80.0;
  static const _lockDistance = 80.0;
  static const _lockHintHeight = 44.0;

  DictationEngine? _engine;
  StreamSubscription<DictationEvent>? _events;
  _Phase _phase = _Phase.idle;
  String _message = '';
  bool _messageIsError = true;
  ({String label, VoidCallback onPressed})? _messageAction;

  /// The pointer holding the mic, or null when no finger is on it. Cleared on
  /// lock, so the locking finger's later moves and release are ignored.
  int? _holdPointer;
  Offset _holdStart = Offset.zero;
  bool _cancelArmed = false;
  bool _locked = false;

  /// How far the finger has risen toward the lock, from 0 to 1.
  double _lockProgress = 0;

  /// How far the finger has slid toward cancel, from 0 to 1.
  double _cancelProgress = 0;

  /// A tap on the stop button of a hands-free recording.
  int? _stopPointer;
  bool _heldLongEnough = false;
  Timer? _minHoldTimer;

  /// The field before the run's first text, and the field as the run last
  /// wrote it. Each new text is spliced into [_base], so a live engine's
  /// revisions replace each other instead of piling up.
  TextEditingValue? _base;
  TextEditingValue? _written;

  final _clock = Stopwatch();
  Timer? _ticker;
  StreamSubscription<double>? _levelSub;
  bool _metered = false;
  final _levels = <double>[]; // rolling window, newest last
  Duration _clipLength = Duration.zero; // kept for the transcribing label

  final _stack = GlobalKey();
  final _row = GlobalKey();
  bool _rowShown = false;

  /// The mic's offset from the field's bottom edge that centers it on the last
  /// text line. The field's bottom padding depends on its border style, so it
  /// is measured after layout rather than assumed.
  double _micBottom = 0;

  @override
  void initState() {
    super.initState();
    widget.controller.addListener(_onFieldChanged);
  }

  @override
  void didUpdateWidget(_Dictation old) {
    super.didUpdateWidget(old);
    if (old.controller != widget.controller) {
      old.controller.removeListener(_onFieldChanged);
      widget.controller.addListener(_onFieldChanged);
    }
  }

  @override
  void dispose() {
    widget.controller.removeListener(_onFieldChanged);
    // Also stops an in-flight run, so leaving the sheet mid-dictation does not
    // leave the mic hot.
    _endRun();
    super.dispose();
  }

  bool get _live => _engine?.live ?? false;

  void _stopMeters() {
    _clock.stop();
    _minHoldTimer?.cancel();
    _minHoldTimer = null;
    _ticker?.cancel();
    _ticker = null;
    _levelSub?.cancel();
    _levelSub = null;
  }

  void _endRun() {
    _stopMeters();
    _events?.cancel();
    _events = null;
    _engine?.dispose();
    _engine = null;
    _base = null;
    _written = null;
  }

  /// [held] is true when a finger on the mic started this run, and false for
  /// a screen reader tap, which has no release to end it.
  Future<void> _start({required bool held}) async {
    _endRun();
    final engine = _engine = ref.read(dictationEngineProvider)();
    _events = engine.events.listen(_onEvent);
    try {
      await engine.start();
      if (!identical(engine, _engine)) return;
      _levels.clear();
      _clock
        ..reset()
        ..start();
      _ticker = Timer.periodic(_tick, (_) {
        if (mounted) setState(() {});
      });
      // Amplitude is best effort. If the platform withholds it the meter stays
      // flat, but the clock still proves the recording is live.
      _levelSub = engine.levels(_tick)?.listen(_onLevel, onError: (_) {});
      _metered = _levelSub != null;
      _heldLongEnough = false;
      _minHoldTimer = Timer(_minHold, () => _heldLongEnough = true);
      if (mounted) setState(() => _phase = _Phase.recording);
      // The finger lifted while the engine was starting, for example during
      // the first-use permission prompt.
      if (held && _holdPointer == null && !_locked) _release();
    } on DictationDenied catch (e) {
      if (!identical(engine, _engine)) return;
      _endRun();
      _showMessage(e.message);
    } catch (e) {
      if (!identical(engine, _engine)) return;
      // Another app holding the mic, or no recognizer on this platform. Stay
      // idle so the next press can retry.
      _endRun();
      _showMessage(e is PlatformException ? e.message ?? e.code : '$e');
    }
  }

  void _onLevel(double level) {
    _levels.add(level);
    if (_levels.length > _maxLevels) _levels.removeAt(0);
  }

  void _onEvent(DictationEvent e) {
    if (_phase != _Phase.recording && _phase != _Phase.transcribing) return;
    switch (e) {
      case DictationText(:final text):
        _write(text.trim());
      case DictationDone():
        final heard = _written != null && _written!.text != _base!.text;
        _endRun();
        if (heard) {
          setState(() => _phase = _Phase.idle);
        } else {
          _showMessage('No speech detected');
        }
      case DictationFailed(:final error):
        _endRun();
        final packs = ref.read(speechPacksProvider);
        _showMessage(
          '$error',
          action: error is SpeechPackMissing && packs.available
              ? (
                  label: 'Download',
                  onPressed: () => _downloadPack(error.language),
                )
              : null,
        );
    }
  }

  /// The base is taken at the first text, not at the start, so typing during
  /// an OpenRouter recording is kept.
  void _write(String text) {
    final base = _base ??= widget.controller.value;
    final value = text.isEmpty ? base : insertAtCursor(base, text);
    _written = value;
    widget.controller.value = value;
    widget.onChanged?.call(value.text);
  }

  /// Typing over a live run would be overwritten by its next text, so an edit
  /// ends the run and keeps what the field holds.
  void _onFieldChanged() {
    final written = _written;
    if (written == null || widget.controller.text == written.text) return;
    _endRun();
    setState(() => _phase = _Phase.idle);
  }

  /// Ends the run and takes its text back out of the field. A transcription in
  /// flight is already billed; this only stops its text from landing.
  void _discard() {
    final base = _base;
    final written = _written;
    _endRun();
    setState(() => _phase = _Phase.idle);
    if (base != null &&
        written != null &&
        widget.controller.text == written.text) {
      widget.controller.value = base;
      widget.onChanged?.call(base.text);
    }
  }

  Future<void> _stop() async {
    final engine = _engine;
    if (engine == null) return;
    _clipLength = _clock.elapsed;
    _stopMeters();
    setState(() => _phase = _Phase.transcribing);
    try {
      await engine.stop();
    } catch (e) {
      if (!mounted || !identical(engine, _engine)) return;
      _endRun();
      _showMessage('$e');
    }
  }

  Future<void> _downloadPack(String language) async {
    _showMessage('Requesting the speech pack…', error: false);
    final result = await ref.read(speechPacksProvider).download(language);
    _showMessage(
      packDownloadMessage(result),
      error: result != PackDownload.started,
    );
  }

  /// Shown in the status row rather than a snackbar: the hosts are modal, and
  /// the root ScaffoldMessenger shows snackbars on the page beneath them.
  void _showMessage(
    String msg, {
    bool error = true,
    ({String label, VoidCallback onPressed})? action,
  }) {
    if (!mounted) return;
    setState(() {
      _phase = _Phase.message;
      _message = msg;
      _messageIsError = error;
      _messageAction = action;
    });
  }

  void _revealRow() {
    final row = _row.currentContext;
    if (row == null) return;
    Scrollable.ensureVisible(
      row,
      duration: const Duration(milliseconds: 150),
      alignmentPolicy: widget.statusBelow
          ? ScrollPositionAlignmentPolicy.keepVisibleAtEnd
          : ScrollPositionAlignmentPolicy.keepVisibleAtStart,
    );
  }

  void _dismissMessage() => setState(() => _phase = _Phase.idle);

  void _onPointerDown(PointerDownEvent e) {
    if (_holdPointer != null) return;
    switch (_phase) {
      case _Phase.idle || _Phase.message:
        _holdPointer = e.pointer;
        _holdStart = e.position;
        _cancelArmed = false;
        _locked = false;
        _lockProgress = 0;
        _cancelProgress = 0;
        HapticFeedback.lightImpact();
        _start(held: true);
      case _Phase.recording:
        _stopPointer = e.pointer;
      case _Phase.transcribing:
        break;
    }
  }

  void _onPointerMove(PointerMoveEvent e) {
    if (e.pointer != _holdPointer) return;
    final delta = e.position - _holdStart;
    // Toward the status row: up when it sits above the field, down when below.
    final towardLock = widget.statusBelow ? delta.dy : -delta.dy;
    if (!_cancelArmed && towardLock > _lockDistance) {
      HapticFeedback.mediumImpact();
      setState(() {
        _holdPointer = null;
        _locked = true;
        _lockProgress = 0;
      });
      return;
    }
    final armed = delta.dx < -_cancelDistance;
    if (armed && !_cancelArmed) HapticFeedback.selectionClick();
    final lock = armed ? 0.0 : (towardLock / _lockDistance).clamp(0.0, 1.0);
    final cancel = (-delta.dx / _cancelDistance).clamp(0.0, 1.0);
    if (armed == _cancelArmed &&
        lock == _lockProgress &&
        cancel == _cancelProgress) {
      return;
    }
    setState(() {
      _cancelArmed = armed;
      _lockProgress = lock;
      _cancelProgress = cancel;
    });
  }

  void _onPointerUp(PointerUpEvent e) {
    if (e.pointer == _stopPointer) {
      _stopPointer = null;
      if (_phase == _Phase.recording) _stop();
      return;
    }
    if (e.pointer != _holdPointer) return;
    _holdPointer = null;
    // Still starting: _start sees the cleared pointer and releases then.
    if (_phase == _Phase.recording) _release();
  }

  void _onPointerCancel(PointerCancelEvent e) {
    if (e.pointer == _stopPointer) _stopPointer = null;
    if (e.pointer != _holdPointer) return;
    _holdPointer = null;
    if (_phase == _Phase.recording) _discard();
  }

  void _release() {
    if (_cancelArmed) {
      _discard();
    } else if (!_heldLongEnough) {
      _discard();
      _showMessage('Hold to record', error: false);
    } else {
      _stop();
    }
  }

  void _onSemanticTap() {
    switch (_phase) {
      case _Phase.idle || _Phase.message:
        _start(held: false);
      case _Phase.recording:
        _stop();
      case _Phase.transcribing:
        break;
    }
  }

  /// One IconButton slot.
  static const _slot = 48.0;

  /// The gap between an IconButton's edge and its 24px icon.
  static const _iconInset = (_slot - 24) / 2;

  /// The status row is shorter than a slot so it adds little above itself, and
  /// [_rowGap] keeps it clear of the field's floating label below.
  static const _rowHeight = 24.0;
  static const _rowGap = 12.0;

  void _alignMic(Duration _) {
    if (!mounted) return;
    final stack = _stack.currentContext?.findRenderObject() as RenderBox?;
    final editable = stack == null ? null : _findEditable(stack);
    if (stack == null || editable == null) return;
    final lineBottom = editable
        .localToGlobal(Offset(0, editable.size.height), ancestor: stack)
        .dy;
    final bottom = stack.size.height -
        lineBottom +
        editable.preferredLineHeight / 2 -
        _slot / 2;
    if (bottom != _micBottom) setState(() => _micBottom = bottom);
  }

  static RenderEditable? _findEditable(RenderObject node) {
    if (node is RenderEditable) return node;
    RenderEditable? found;
    node.visitChildren((child) => found ??= _findEditable(child));
    return found;
  }

  @override
  Widget build(BuildContext context) {
    const dim = TextStyle(color: AppColors.dim, fontSize: 12);
    WidgetsBinding.instance.addPostFrameCallback(_alignMic);

    final error = Theme.of(context).colorScheme.error;
    final holding = _phase == _Phase.recording && _holdPointer != null;

    // One Listener whatever the phase, so the pointer that started a recording
    // keeps reporting to it after the icon changes underneath.
    final primary = Semantics(
      key: const Key('voice-input'),
      button: true,
      excludeSemantics: true,
      label: switch (_phase) {
        _Phase.recording => _live ? 'Stop' : 'Stop and transcribe',
        _Phase.transcribing => 'Transcribing',
        _ => 'Dictate',
      },
      onTap: _phase == _Phase.transcribing ? null : _onSemanticTap,
      child: Listener(
        behavior: HitTestBehavior.opaque,
        onPointerDown: _onPointerDown,
        onPointerMove: _onPointerMove,
        onPointerUp: _onPointerUp,
        onPointerCancel: _onPointerCancel,
        // Claims the gesture arena at once, so a scrollable around the field
        // does not take a slide toward the lock as a scroll.
        child: RawGestureDetector(
          behavior: HitTestBehavior.opaque,
          excludeFromSemantics: true,
          gestures: {
            EagerGestureRecognizer:
                GestureRecognizerFactoryWithHandlers<EagerGestureRecognizer>(
              EagerGestureRecognizer.new,
              (_) {},
            ),
          },
          child: SizedBox(
            width: _slot,
            height: _slot,
            child: Center(
              child: switch (_phase) {
                _Phase.idle || _Phase.message => const Icon(Icons.mic_none),
                _Phase.recording when holding => Transform.translate(
                    offset: Offset(
                      0,
                      (widget.statusBelow ? 1 : -1) *
                          _lockProgress *
                          _iconInset,
                    ),
                    child: Icon(
                      Icons.mic,
                      color: _cancelArmed ? AppColors.dim : error,
                    ),
                  ),
                _Phase.recording => Icon(Icons.stop_circle, color: error),
                _Phase.transcribing => const SizedBox(
                    width: 20,
                    height: 20,
                    child: CircularProgressIndicator(strokeWidth: 2),
                  ),
              },
            ),
          ),
        ),
      ),
    );

    final status = switch (_phase) {
      _Phase.idle => null,
      _Phase.recording => Row(
          children: [
            Text(
              formatClock(_clock.elapsed),
              key: const Key('voice-clock'),
              style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
            ),
            const SizedBox(width: 10),
            Expanded(
              child: _metered ? LevelMeter(levels: _levels) : const SizedBox(),
            ),
            if (holding) ...[
              const SizedBox(width: 10),
              if (_cancelArmed)
                Text(
                  'Release to cancel',
                  style: TextStyle(color: error, fontSize: 12),
                )
              else
                _SlideHint(
                  key: const Key('voice-slide-hint'),
                  progress: _cancelProgress,
                  color: error,
                ),
            ],
          ],
        ),
      _Phase.transcribing => Text(
          _live
              ? 'Finishing…'
              : 'Transcribing ${formatClock(_clipLength)} of audio…',
          key: const Key('voice-status'),
          style: dim,
        ),
      _Phase.message => Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Flexible(
              child: Text(
                _message,
                key: const Key('voice-message'),
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: _messageIsError
                    ? TextStyle(color: error, fontSize: 12)
                    : dim,
              ),
            ),
            if (_messageAction case final action?)
              TextButton(
                key: const Key('voice-message-action'),
                style: TextButton.styleFrom(
                  padding: const EdgeInsets.symmetric(horizontal: 8),
                  minimumSize: const Size(0, _rowHeight),
                  tapTargetSize: MaterialTapTargetSize.shrinkWrap,
                  textStyle: const TextStyle(fontSize: 12),
                ),
                onPressed: action.onPressed,
                child: Text(action.label),
              ),
          ],
        ),
    };

    // A field near the keyboard leaves no room for the row to appear in view.
    if (status != null && !_rowShown) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _revealRow());
    }
    _rowShown = status != null;
    final row = status == null
        ? const SizedBox.shrink()
        : Padding(
            key: _row,
            padding: widget.statusBelow
                ? const EdgeInsets.only(top: _rowGap)
                : const EdgeInsets.only(bottom: _rowGap),
            child: SizedBox(
              height: _rowHeight,
              child: Row(
                children: [
                  Expanded(
                    child: Padding(
                      // With the trash can hidden, its slot stays clear for the
                      // lock hint that floats up from the mic.
                      padding: EdgeInsets.only(
                        left: _iconInset,
                        right: holding ? _slot : 0,
                      ),
                      child: Align(
                        alignment: Alignment.centerLeft,
                        child: status,
                      ),
                    ),
                  ),
                  // Same slot width as the primary button, so the two stack in
                  // one column at the field's right edge. Hidden while a finger
                  // holds the mic, where sliding left does its job.
                  if (!holding)
                    IconButton(
                      key: const Key('voice-cancel'),
                      constraints: const BoxConstraints.tightFor(
                        width: _slot,
                        height: _rowHeight,
                      ),
                      padding: EdgeInsets.zero,
                      icon: const Icon(Icons.delete_outline, size: 20),
                      tooltip: switch (_phase) {
                        _Phase.recording => 'Discard recording',
                        _Phase.message => 'Dismiss',
                        _ => 'Discard transcript',
                      },
                      onPressed:
                          _phase == _Phase.message ? _dismissMessage : _discard,
                    ),
                ],
              ),
            ),
          );
    final field = Stack(
      key: _stack,
      // The lock hint reaches past the field's edges.
      clipBehavior: Clip.none,
      children: [
        widget.field(const SizedBox(width: _slot)),
        Positioned(right: 0, bottom: _micBottom, child: primary),
        if (holding && !_cancelArmed)
          Positioned(
            right: 0,
            bottom: widget.statusBelow
                ? _micBottom - _lockHintHeight
                : _micBottom + _slot,
            width: _slot,
            height: _lockHintHeight,
            child: _LockHint(
              key: const Key('voice-lock-hint'),
              progress: _lockProgress,
              down: widget.statusBelow,
            ),
          ),
      ],
    );
    // The row is always present, even as an empty box, so it appearing does
    // not move the field to a new slot, which would rebuild it and drop focus.
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: widget.statusBelow ? [field, row] : [row, field],
    );
  }
}

String packDownloadMessage(PackDownload result) => switch (result) {
      PackDownload.started =>
        'Downloading the speech pack. Try again when it finishes.',
      PackDownload.unsupported =>
        'This device cannot download speech packs.',
      PackDownload.failed => 'The speech pack download failed.',
    };

/// Points the way to lock: an arrow leading from the mic to a padlock, up or
/// [down]. Fills from the mic's side as [progress] runs from 0 to 1.
class _LockHint extends StatelessWidget {
  const _LockHint({
    super.key,
    required this.progress,
    required this.down,
  });

  final double progress;
  final bool down;

  @override
  Widget build(BuildContext context) {
    final color = Color.lerp(AppColors.dim, AppColors.accent, progress);
    return Center(
      child: ClipRRect(
        borderRadius: BorderRadius.circular(16),
        child: ColoredBox(
          color: Theme.of(context).colorScheme.surfaceContainerHighest,
          child: Stack(
            children: [
              Positioned.fill(
                child: Align(
                  alignment:
                      down ? Alignment.topCenter : Alignment.bottomCenter,
                  child: FractionallySizedBox(
                    key: const Key('voice-lock-fill'),
                    heightFactor: progress,
                    widthFactor: 1,
                    child: ColoredBox(
                      color: AppColors.accent.withValues(alpha: 0.3),
                    ),
                  ),
                ),
              ),
              Padding(
                padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 6),
                child: Column(
                  mainAxisSize: MainAxisSize.min,
                  children: down
                      ? [
                          Icon(Icons.keyboard_arrow_down, size: 16, color: color),
                          Icon(Icons.lock_outline, size: 16, color: color),
                        ]
                      : [
                          Icon(Icons.lock_outline, size: 16, color: color),
                          Icon(Icons.keyboard_arrow_up, size: 16, color: color),
                        ],
                ),
              ),
            ],
          ),
        ),
      ),
    );
  }
}

/// "Slide to cancel" with a leading arrow. Lights up in [color] from the right
/// edge as [progress] runs from 0 to 1, following the finger.
class _SlideHint extends StatelessWidget {
  const _SlideHint({super.key, required this.progress, required this.color});

  final double progress;
  final Color color;

  @override
  Widget build(BuildContext context) => ShaderMask(
        blendMode: BlendMode.srcIn,
        shaderCallback: (bounds) => LinearGradient(
          begin: Alignment.centerRight,
          end: Alignment.centerLeft,
          colors: [color, color, AppColors.dim, AppColors.dim],
          stops: [0, progress, progress, 1],
        ).createShader(bounds),
        child: const Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.arrow_back, size: 14),
            SizedBox(width: 4),
            Text('Slide to cancel', style: TextStyle(fontSize: 12)),
          ],
        ),
      );
}

/// `m:ss`, the readout on the recording clock.
String formatClock(Duration d) =>
    '${d.inMinutes}:${(d.inSeconds % 60).toString().padLeft(2, '0')}';

/// The recent level history as a row of bars, newest on the right. Shows as
/// many bars as fit the width it is given.
class LevelMeter extends StatelessWidget {
  const LevelMeter({super.key, required this.levels});

  final List<double> levels;

  // A painter rather than a LayoutBuilder: the spawn dialog is an AlertDialog,
  // which asks its content for intrinsic sizes, and LayoutBuilder throws.
  @override
  Widget build(BuildContext context) => CustomPaint(
        size: const Size.fromHeight(16),
        painter: _MeterPainter(levels),
      );
}

class _MeterPainter extends CustomPainter {
  _MeterPainter(this.levels);

  final List<double> levels;

  static const _barWidth = 3.0;
  static const _barPitch = 5.0;

  @override
  void paint(Canvas canvas, Size size) {
    final barCount = (size.width / _barPitch).floor();
    final shown = levels.length > barCount
        ? levels.sublist(levels.length - barCount)
        : levels;
    // Left-pad so the bars scroll in from the right as audio arrives.
    final pad = barCount - shown.length;
    final paint = Paint()..color = AppColors.secondary;
    for (var i = 0; i < barCount; i++) {
      final h = 2 + (size.height - 2) * (i < pad ? 0.0 : shown[i - pad]);
      canvas.drawRRect(
        RRect.fromRectAndRadius(
          Rect.fromLTWH(
            i * _barPitch + 1,
            (size.height - h) / 2,
            _barWidth,
            h,
          ),
          const Radius.circular(1.5),
        ),
        paint,
      );
    }
  }

  // The level list is mutated in place, so there is no old value to compare.
  @override
  bool shouldRepaint(_MeterPainter oldDelegate) => true;
}

/// Splices [text] into [value] at the cursor, replacing any selection, and
/// leaves the cursor directly after it. Pads with a space on either side that
/// would otherwise run into a neighbouring word, so dictating into the middle
/// of a sentence does not produce "onetwo".
TextEditingValue insertAtCursor(TextEditingValue value, String text) {
  final base = value.text;
  // An untouched field reports offset -1; append in that case.
  final start = value.selection.start < 0 ? base.length : value.selection.start;
  final end = value.selection.end < 0 ? base.length : value.selection.end;
  final before = start > 0 && !_space(base[start - 1]) ? ' ' : '';
  final after = end < base.length && !_space(base[end]) ? ' ' : '';
  return TextEditingValue(
    text: base.replaceRange(start, end, '$before$text$after'),
    selection:
        TextSelection.collapsed(offset: start + before.length + text.length),
  );
}

bool _space(String ch) => ' \n\t'.contains(ch);
