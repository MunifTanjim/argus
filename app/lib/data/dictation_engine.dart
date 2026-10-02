import 'dart:async';
import 'dart:io';

import 'package:flutter/foundation.dart';
import 'package:flutter/services.dart';
import 'package:record/record.dart';
import 'package:stts/stts.dart';

import 'openrouter.dart';

sealed class DictationEvent {
  const DictationEvent();
}

/// The full text of the run so far. Each event replaces the one before it.
final class DictationText extends DictationEvent {
  const DictationText(this.text);
  final String text;
}

/// The run ended. No more events follow.
final class DictationDone extends DictationEvent {
  const DictationDone();
}

/// The run failed. No more events follow.
final class DictationFailed extends DictationEvent {
  const DictationFailed(this.error);
  final Object error;
}

/// Thrown by [DictationEngine.start] when the user refused a permission.
class DictationDenied implements Exception {
  const DictationDenied(this.message);
  final String message;

  @override
  String toString() => message;
}

class NoRecognizer implements Exception {
  const NoRecognizer();

  @override
  String toString() => 'No speech recognizer on this device';
}

/// The recognizer has no on-device pack for [language]. Android only.
class SpeechPackMissing implements Exception {
  const SpeechPackMissing(this.language);
  final String language;

  @override
  String toString() => 'Speech pack for $language is not installed';
}

/// One dictation run: built for a run, then disposed.
abstract class DictationEngine {
  /// Whether text arrives while the user speaks, rather than after [stop].
  bool get live;

  Stream<DictationEvent> get events;

  /// Meter levels from 0 to 1, or null when the engine has no audio levels.
  Stream<double>? levels(Duration interval);

  Future<void> start();

  /// Ends the run. The last text and [DictationDone] follow on [events].
  Future<void> stop();

  /// Aborts the run and releases the engine. No more events follow.
  Future<void> dispose();
}

/// Records a wav and uploads it to OpenRouter on [stop].
class OpenRouterEngine implements DictationEngine {
  OpenRouterEngine({
    required this._recorder,
    required this._client,
    required this.apiKey,
    required this.model,
  });

  final AudioRecorder _recorder;
  final OpenRouterClient _client;
  final String apiKey;
  final String model;
  final _events = StreamController<DictationEvent>();
  String? _path;
  bool _disposed = false;

  @override
  bool get live => false;

  @override
  Stream<DictationEvent> get events => _events.stream;

  @override
  Stream<double> levels(Duration interval) =>
      _recorder.onAmplitudeChanged(interval).map((a) => meterLevel(a.current));

  @override
  Future<void> start() async {
    if (!await _recorder.hasPermission()) {
      throw const DictationDenied('Microphone permission denied');
    }
    if (_disposed) return;
    // systemTemp keeps this off the path_provider dependency. The file is
    // deleted as soon as it is uploaded — it holds the user's voice.
    _path =
        '${Directory.systemTemp.path}/argus_voice_'
        '${DateTime.now().microsecondsSinceEpoch}.wav';
    // wav: the one format every OpenRouter transcription provider accepts,
    // and the record package encodes it on every platform without a codec.
    await _recorder.start(
      const RecordConfig(encoder: AudioEncoder.wav),
      path: _path!,
    );
    // Disposed while starting: dispose() ran before there was a recording to
    // cancel.
    if (_disposed) await _recorder.cancel();
  }

  @override
  Future<void> stop() async {
    final path = await _recorder.stop() ?? _path;
    final file = path == null ? null : File(path);
    try {
      if (file == null || !file.existsSync()) {
        _emit(const DictationFailed('Recording failed'));
        return;
      }
      final text = await _client.transcribe(
        apiKey: apiKey,
        model: model,
        audio: await file.readAsBytes(),
      );
      _emit(DictationText(text.trim()));
      _emit(const DictationDone());
    } catch (e) {
      _emit(DictationFailed(e));
    } finally {
      if (file != null && file.existsSync()) {
        try {
          await file.delete();
        } catch (_) {
          // Best effort; the OS clears systemTemp anyway.
        }
      }
    }
  }

  /// A transcription in flight is already billed. Disposing only drops its
  /// text.
  @override
  Future<void> dispose() async {
    _disposed = true;
    _events.close();
    try {
      // cancel() stops the recorder and deletes the file, unlike stop().
      await _recorder.cancel();
    } catch (_) {
      // Nothing to recover: the file is temporary either way.
    }
    await _recorder.dispose();
  }

  void _emit(DictationEvent e) {
    if (!_events.isClosed) _events.add(e);
  }
}

/// Streams text from the OS speech recognizer. The recognizer stops by itself
/// after a pause.
class SystemEngine implements DictationEngine {
  SystemEngine(this._stt, {this.language = ''});

  final Stt _stt;

  /// A recognizer language tag, or empty for the device language.
  final String language;

  /// The recognizer keeps its language for the whole process and reports its
  /// current one, so the device language is read once, before the first run
  /// sets it.
  static Future<String>? _deviceLanguage;

  static Future<String> deviceLanguage(Stt stt) =>
      _deviceLanguage ??= stt.getLanguage();

  @visibleForTesting
  static void resetDeviceLanguage() => _deviceLanguage = null;

  /// How long to wait for the stop event. Android sends none when the
  /// recognizer already stopped by itself without an error.
  static const _stopWait = Duration(seconds: 2);

  String _language = '';

  final _events = StreamController<DictationEvent>();
  StreamSubscription<SttState>? _states;
  StreamSubscription<SttRecognition>? _results;

  /// The native recognizer is one per process and its events reach every
  /// [Stt]. A stop or a result from an earlier run can arrive after this run
  /// starts, so events count only after this run's start event, or once this
  /// run asks to stop.
  bool _started = false;
  bool _stopping = false;
  bool _disposed = false;
  Timer? _stopTimer;

  @override
  bool get live => true;

  @override
  Stream<DictationEvent> get events => _events.stream;

  @override
  Stream<double>? levels(Duration interval) => null;

  @override
  Future<void> start() async {
    if (!await _stt.hasPermission()) {
      throw const DictationDenied(
        'Microphone or speech recognition permission denied',
      );
    }
    // Android starts nothing and reports nothing without a recognizer.
    if (!await _stt.isSupported()) {
      throw const NoRecognizer();
    }
    final device = await deviceLanguage(_stt);
    _language = language.isEmpty ? device : language;
    await _stt.setLanguage(_language);
    if (_disposed) return;
    _states = _stt.onStateChanged.listen(_onState, onError: _onError);
    _results = _stt.onResultChanged.listen(_onResult);
    await _stt.start(const SttRecognitionOptions(punctuation: true));
    if (_disposed) await _stt.stop();
  }

  void _onState(SttState s) {
    switch (s) {
      case SttState.start:
        _started = true;
      case SttState.stop when _started || _stopping:
        _finish();
      default:
        break;
    }
  }

  /// Not gated like the stop event: a failed start sends an error and no start
  /// event, and the platforms detach a stopped run before it can report one.
  void _onError(Object e) {
    _emit(DictationFailed(_describe(e)));
    _events.close();
  }

  /// Android reports SpeechRecognizer error codes; iOS reports its own text.
  Object _describe(Object e) {
    if (e is! PlatformException) return e;
    return switch (e.code) {
      '12' => 'This recognizer does not support $_language',
      '13' => SpeechPackMissing(_language),
      '6' => 'No speech detected',
      '9' => 'Speech recognition permission denied',
      _ => e.message ?? 'Speech recognition failed (${e.code})',
    };
  }

  void _onResult(SttRecognition r) {
    if (_started) _emit(DictationText(r.text));
  }

  @override
  Future<void> stop() async {
    _stopping = true;
    await _stt.stop();
    if (!_events.isClosed) _stopTimer = Timer(_stopWait, _finish);
  }

  void _finish() {
    _stopTimer?.cancel();
    _emit(const DictationDone());
    _events.close();
  }

  @override
  Future<void> dispose() async {
    _disposed = true;
    _stopTimer?.cancel();
    await _states?.cancel();
    await _results?.cancel();
    _events.close();
    await _stt.dispose();
  }

  void _emit(DictationEvent e) {
    if (!_events.isClosed) _events.add(e);
  }
}

enum PackDownload { started, unsupported, failed }

/// Installs the System recognizer's on-device language packs. Android 14+.
class SpeechPacks {
  SpeechPacks(
    this._newStt, [
    this._channel = const MethodChannel('dev.muniftanjim.argus/speech'),
  ]);

  final Stt Function() _newStt;

  /// MainActivity.kt: stts cannot tell which packs are installed.
  final MethodChannel _channel;

  /// How long to wait for a download that fails at once. A started download
  /// may never report its end, so silence counts as started.
  static const _wait = Duration(seconds: 2);

  bool get available => defaultTargetPlatform == TargetPlatform.android;

  /// The language a System run uses for [selected].
  Future<String> resolve(String selected) async =>
      selected.isEmpty ? SystemEngine.deviceLanguage(_newStt()) : selected;

  /// Whether [language]'s pack is installed, or null when the device cannot
  /// download packs (below Android 14, or no on-device recognizer).
  Future<bool?> isInstalled(String language) async {
    if (!available) return null;
    final List<Object?>? installed;
    try {
      installed = await _channel.invokeListMethod('installedLanguages');
    } on PlatformException {
      return null;
    }
    if (installed == null) return null;
    final wanted = _normalize(language);
    return installed.any((l) => _normalize('$l') == wanted);
  }

  static String _normalize(String tag) =>
      tag.replaceAll('_', '-').toLowerCase();

  /// The [Stt] is not disposed: disposing stops any live recognition, and
  /// nothing was started here.
  Future<PackDownload> download(String language) async {
    final android = _newStt().android;
    if (android == null) return PackDownload.unsupported;
    final end = Completer<int?>();
    android.onDownloadModelEnd((_, err) {
      if (!end.isCompleted) end.complete(err);
    });
    try {
      await android.downloadModel(language);
      final err = await end.future.timeout(_wait, onTimeout: () => null);
      return switch (err) {
        null => PackDownload.started,
        // SpeechRecognizer.ERROR_CANNOT_CHECK_SUPPORT: below Android 14, or
        // no on-device recognizer.
        14 => PackDownload.unsupported,
        _ => PackDownload.failed,
      };
    } finally {
      android.onDownloadModelEnd(null);
    }
  }
}

/// Maps a dBFS reading to a 0..1 bar height. A phone mic floors around -45 dB
/// in a quiet room, so anchor silence there rather than at the -160 the
/// platform reports for digital zero.
double meterLevel(double dbfs) {
  if (!dbfs.isFinite) return 0;
  return ((dbfs + 45) / 45).clamp(0.0, 1.0);
}
