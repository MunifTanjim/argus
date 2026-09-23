import 'dart:async';
import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:record/record.dart';

import '../state/voice.dart';
import 'theme.dart';

/// The dictation bar for [controller], or null when voice input is off.
///
/// Place it directly under the field as a null-aware element:
/// `?voiceInputBar(ref, _text)`. It is a full-width row rather than a suffix
/// icon because it has to show the elapsed time, a level meter and the
/// transcribing state — none of which fit inside a text field's suffix.
Widget? voiceInputBar(
  WidgetRef ref,
  TextEditingController controller, {
  ValueChanged<String>? onChanged,
  bool autoStart = false,
}) =>
    ref.watch(voicePrefsProvider.select((p) => p.enabled))
        ? VoiceInputBar(
            controller: controller,
            onChanged: onChanged,
            autoStart: autoStart,
          )
        : null;

enum _Phase { idle, recording, transcribing }

/// Records, transcribes through OpenRouter, and drops the text into the field
/// at the cursor. It never sends: the user reads and edits the transcript, then
/// presses the field's own send button.
class VoiceInputBar extends ConsumerStatefulWidget {
  const VoiceInputBar({
    super.key,
    required this.controller,
    this.onChanged,
    this.autoStart = false,
  });

  final TextEditingController controller;

  /// Mirrors the field's own `onChanged`. Dictation bypasses the keyboard, so
  /// fields that track their text separately (drafts, validation) need telling.
  final ValueChanged<String>? onChanged;

  /// Starts recording as soon as the bar is on screen, for callers that already
  /// took a deliberate "dictate" tap elsewhere.
  final bool autoStart;

  @override
  ConsumerState<VoiceInputBar> createState() => _VoiceInputBarState();
}

class _VoiceInputBarState extends ConsumerState<VoiceInputBar> {
  /// Drives both the clock readout and the meter. 200ms is fast enough to look
  /// live and slow enough not to churn the widget tree.
  static const _tick = Duration(milliseconds: 200);
  static const _barCount = 14;

  AudioRecorder? _recorder;
  _Phase _phase = _Phase.idle;
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

  @override
  void initState() {
    super.initState();
    // Post-frame: _start can show a snackbar on a denied permission, and
    // ScaffoldMessenger is not reachable until the sheet is laid out.
    if (widget.autoStart) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _start();
      });
    }
  }

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
    _ticker?.cancel();
    _ticker = null;
    _amplitudes?.cancel();
    _amplitudes = null;
  }

  Future<void> _start() async {
    try {
      // Built on first tap, not in initState: constructing a recorder reaches
      // for the platform channel, and most builds of this widget never record.
      final recorder = _recorder ??= AudioRecorder();
      if (!await recorder.hasPermission()) {
        _snack('Microphone permission denied');
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
      if (mounted) setState(() => _phase = _Phase.recording);
    } catch (e) {
      // Another app holding the mic, or no recorder on this platform. Stay in
      // the idle state so the next tap can retry.
      _stopMeters();
      _snack('Could not start recording: $e');
    }
  }

  void _onAmplitude(Amplitude a) {
    _levels.add(meterLevel(a.current));
    if (_levels.length > _barCount) _levels.removeAt(0);
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
  /// text from landing in the field and frees the bar for another take.
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
      if (file == null || !file.existsSync()) throw Exception('recording failed');
      final prefs = ref.read(voicePrefsProvider);
      final text = await ref.read(openRouterClientProvider).transcribe(
            apiKey: prefs.apiKey,
            model: prefs.model,
            audio: await file.readAsBytes(),
          );
      if (!mounted || gen != _generation) return; // discarded or superseded
      final trimmed = text.trim();
      if (trimmed.isEmpty) {
        _snack('No speech detected');
      } else {
        widget.controller.value = insertAtCursor(widget.controller.value, trimmed);
        widget.onChanged?.call(widget.controller.text);
      }
    } catch (e) {
      // Stay quiet about a run the user already walked away from.
      if (!mounted || gen != _generation) return;
      _snack('Transcription failed: $e');
    } finally {
      if (file != null && file.existsSync()) {
        try {
          await file.delete();
        } catch (_) {
          // Best effort; the OS clears systemTemp anyway.
        }
      }
      if (mounted && gen == _generation) setState(() => _phase = _Phase.idle);
    }
  }

  void _snack(String msg) {
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));
  }

  /// One IconButton slot.
  static const _slot = 48.0;

  @override
  Widget build(BuildContext context) {
    const dim = TextStyle(color: AppColors.dim, fontSize: 12);
    final idle = _phase == _Phase.idle;

    final primary = switch (_phase) {
      _Phase.idle => IconButton(
          key: const Key('voice-input'),
          icon: const Icon(Icons.mic_none),
          tooltip: 'Dictate',
          onPressed: _start,
        ),
      _Phase.recording => IconButton(
          key: const Key('voice-input'),
          icon: Icon(
            Icons.stop_circle,
            color: Theme.of(context).colorScheme.error,
          ),
          tooltip: 'Stop and transcribe',
          onPressed: _stopAndTranscribe,
        ),
      _Phase.transcribing => const SizedBox(
          width: _slot,
          height: _slot,
          child: Center(
            child: SizedBox(
              width: 20,
              height: 20,
              child: CircularProgressIndicator(strokeWidth: 2),
            ),
          ),
        ),
    };

    final body = switch (_phase) {
      _Phase.idle => const Text('Dictate', style: dim),
      _Phase.recording => Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(
              formatClock(_clock.elapsed),
              key: const Key('voice-clock'),
              style: const TextStyle(fontFamily: 'monospace', fontSize: 12),
            ),
            const SizedBox(width: 10),
            _Meter(levels: _levels, barCount: _barCount),
          ],
        ),
      _Phase.transcribing => Text(
          'Transcribing ${formatClock(_clipLength)} of audio…',
          key: const Key('voice-status'),
          style: dim,
        ),
    };

    // Right-handed layout. The primary control never moves: the mic becomes
    // stop in the same spot, so the common path is tap, talk, tap again without
    // the thumb travelling. Cancel sits on the far side on purpose — it is the
    // rare action, and a mis-tap there would throw away what was just said.
    return SizedBox(
      height: _slot,
      child: Row(
        children: [
          ?(idle
              ? null
              : IconButton(
                  key: const Key('voice-cancel'),
                  icon: const Icon(Icons.close),
                  tooltip: _phase == _Phase.recording
                      ? 'Discard recording'
                      : 'Discard transcript',
                  onPressed: _phase == _Phase.recording
                      ? _cancelRecording
                      : _discardTranscription,
                )),
          // Right-aligned so the clock and meter always sit against the primary
          // button, wherever the left slot is filled or not.
          Expanded(
            child: Align(alignment: Alignment.centerRight, child: body),
          ),
          primary,
        ],
      ),
    );
  }
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

/// The recent level history as a row of bars, newest on the right.
class _Meter extends StatelessWidget {
  const _Meter({required this.levels, required this.barCount});

  final List<double> levels;
  final int barCount;

  @override
  Widget build(BuildContext context) {
    // Left-pad so the bars scroll in from the right as audio arrives.
    final pad = barCount - levels.length;
    return SizedBox(
      height: 16,
      child: Row(
        children: [
          for (var i = 0; i < barCount; i++)
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 1),
              child: Container(
                width: 3,
                height: 2 + 14 * (i < pad ? 0.0 : levels[i - pad]),
                decoration: BoxDecoration(
                  color: AppColors.secondary,
                  borderRadius: BorderRadius.circular(1.5),
                ),
              ),
            ),
        ],
      ),
    );
  }
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
