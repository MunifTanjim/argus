import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:record/record.dart';

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

/// Records, transcribes through OpenRouter, and drops the text into the field
/// at the cursor. It never sends: the user reads and edits the transcript, then
/// presses the field's own send button.
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

  AudioRecorder? _recorder;
  _Phase _phase = _Phase.idle;
  String _message = '';
  bool _messageIsError = true;

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
  String? _path;

  /// Bumped whenever a run is claimed, cancelled or superseded. A transcription
  /// compares the generation it started under against this before it touches
  /// the field, so a discarded or restarted run cannot land late.
  int _generation = 0;

  final _clock = Stopwatch();
  Timer? _ticker;
  StreamSubscription<Amplitude>? _amplitudes;
  final _levels = <double>[]; // rolling window, newest last
  Duration _clipLength = Duration.zero; // kept for the transcribing label

  final _stack = GlobalKey();

  /// The mic's offset from the field's bottom edge that centers it on the last
  /// text line. The field's bottom padding depends on its border style, so it
  /// is measured after layout rather than assumed.
  double _micBottom = 0;

  @override
  void dispose() {
    _stopMeters();
    // Also stops an in-flight recording, so leaving the sheet mid-dictation
    // does not leave the mic hot.
    _recorder?.dispose();
    super.dispose();
  }

  void _stopMeters() {
    _clock.stop();
    _minHoldTimer?.cancel();
    _minHoldTimer = null;
    _ticker?.cancel();
    _ticker = null;
    _amplitudes?.cancel();
    _amplitudes = null;
  }

  /// [held] is true when a finger on the mic started this run, and false for
  /// a screen reader tap, which has no release to end it.
  Future<void> _start({required bool held}) async {
    try {
      // Built on first press, not in initState: constructing a recorder reaches
      // for the platform channel, and most builds of this widget never record.
      final recorder = _recorder ??= ref.read(audioRecorderProvider)();
      if (!await recorder.hasPermission()) {
        _showMessage('Microphone permission denied');
        return;
      }
      // systemTemp keeps this off the path_provider dependency. The file is
      // deleted as soon as it is uploaded — it holds the user's voice.
      _path = '${Directory.systemTemp.path}/argus_voice_'
          '${DateTime.now().microsecondsSinceEpoch}.wav';
      // wav: the one format every OpenRouter transcription provider accepts,
      // and the record package encodes it on every platform without a codec.
      await recorder.start(
        const RecordConfig(encoder: AudioEncoder.wav),
        path: _path!,
      );
      _levels.clear();
      _clock
        ..reset()
        ..start();
      _ticker = Timer.periodic(_tick, (_) {
        if (mounted) setState(() {});
      });
      // Amplitude is best effort. If the platform withholds it the meter stays
      // flat, but the clock still proves the recording is live.
      _amplitudes = recorder
          .onAmplitudeChanged(_tick)
          .listen(_onAmplitude, onError: (_) {});
      _heldLongEnough = false;
      _minHoldTimer = Timer(_minHold, () => _heldLongEnough = true);
      if (mounted) setState(() => _phase = _Phase.recording);
      // The finger lifted while the recorder was starting, for example during
      // the first-use permission prompt.
      if (held && _holdPointer == null && !_locked) _release();
    } catch (e) {
      // Another app holding the mic, or no recorder on this platform. Stay
      // idle so the next press can retry.
      _stopMeters();
      _showMessage('Could not start recording: $e');
    }
  }

  void _onAmplitude(Amplitude a) {
    _levels.add(meterLevel(a.current));
    if (_levels.length > _maxLevels) _levels.removeAt(0);
  }

  /// Throws the recording away. Nothing is uploaded, so nothing is billed.
  Future<void> _cancelRecording() async {
    _stopMeters();
    _generation++;
    setState(() => _phase = _Phase.idle);
    try {
      // cancel() stops the recorder and deletes the file, unlike stop().
      await _recorder?.cancel();
    } catch (_) {
      // Nothing to recover: the file is temporary either way.
    }
    _path = null;
  }

  /// Drops a transcription that is already in flight.
  ///
  /// The request has been sent and OpenRouter will bill it. This only stops the
  /// text from landing in the field and frees the mic for another take.
  void _discardTranscription() {
    _generation++;
    setState(() => _phase = _Phase.idle);
  }

  Future<void> _stopAndTranscribe() async {
    final gen = ++_generation;
    _clipLength = _clock.elapsed;
    _stopMeters();
    setState(() => _phase = _Phase.transcribing);
    final path = await _recorder?.stop() ?? _path;
    final file = path == null ? null : File(path);
    try {
      if (file == null || !file.existsSync()) {
        _showMessage('Recording failed');
        return;
      }
      final prefs = ref.read(voicePrefsProvider);
      final text = await ref.read(openRouterClientProvider).transcribe(
            apiKey: prefs.apiKey,
            model: prefs.model,
            audio: await file.readAsBytes(),
          );
      if (!mounted || gen != _generation) return; // discarded or superseded
      final trimmed = text.trim();
      if (trimmed.isEmpty) {
        _showMessage('No speech detected');
      } else {
        widget.controller.value = insertAtCursor(widget.controller.value, trimmed);
        widget.onChanged?.call(widget.controller.text);
      }
    } catch (e) {
      // Stay quiet about a run the user already walked away from.
      if (!mounted || gen != _generation) return;
      _showMessage('$e');
    } finally {
      if (file != null && file.existsSync()) {
        try {
          await file.delete();
        } catch (_) {
          // Best effort; the OS clears systemTemp anyway.
        }
      }
      if (mounted && gen == _generation && _phase == _Phase.transcribing) {
        setState(() => _phase = _Phase.idle);
      }
    }
  }

  /// Shown in the status row rather than a snackbar: the hosts are modal, and
  /// the root ScaffoldMessenger shows snackbars on the page beneath them.
  void _showMessage(String msg, {bool error = true}) {
    if (!mounted) return;
    setState(() {
      _phase = _Phase.message;
      _message = msg;
      _messageIsError = error;
    });
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
      if (_phase == _Phase.recording) _stopAndTranscribe();
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
    if (_phase == _Phase.recording) _cancelRecording();
  }

  void _release() {
    if (_cancelArmed) {
      _cancelRecording();
    } else if (!_heldLongEnough) {
      _cancelRecording();
      _showMessage('Hold to record', error: false);
    } else {
      _stopAndTranscribe();
    }
  }

  void _onSemanticTap() {
    switch (_phase) {
      case _Phase.idle || _Phase.message:
        _start(held: false);
      case _Phase.recording:
        _stopAndTranscribe();
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
        _Phase.recording => 'Stop and transcribe',
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
        child: SizedBox(
          width: _slot,
          height: _slot,
          child: Center(
            child: switch (_phase) {
              _Phase.idle || _Phase.message => const Icon(Icons.mic_none),
              _Phase.recording when holding => Transform.translate(
                  offset: Offset(
                    0,
                    (widget.statusBelow ? 1 : -1) * _lockProgress * _iconInset,
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
            Expanded(child: LevelMeter(levels: _levels)),
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
          'Transcribing ${formatClock(_clipLength)} of audio…',
          key: const Key('voice-status'),
          style: dim,
        ),
      _Phase.message => Text(
          _message,
          key: const Key('voice-message'),
          maxLines: 1,
          overflow: TextOverflow.ellipsis,
          style: _messageIsError
              ? TextStyle(color: error, fontSize: 12)
              : dim,
        ),
    };

    final row = status == null
        ? const SizedBox.shrink()
        : Padding(
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
                      onPressed: switch (_phase) {
                        _Phase.recording => _cancelRecording,
                        _Phase.message => _dismissMessage,
                        _ => _discardTranscription,
                      },
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

/// Maps a dBFS reading to a 0..1 bar height. A phone mic floors around -45 dB
/// in a quiet room, so anchor silence there rather than at the -160 the
/// platform reports for digital zero.
double meterLevel(double dbfs) {
  if (!dbfs.isFinite) return 0;
  return ((dbfs + 45) / 45).clamp(0.0, 1.0);
}

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
