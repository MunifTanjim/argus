import 'dart:async';

import 'package:argus/data/dictation_engine.dart';
import 'package:argus/data/openrouter.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:record/record.dart';
import 'package:stts/stts.dart';

class _FakeRecorder extends Fake implements AudioRecorder {
  _FakeRecorder({this.permitted = true, this.gate});

  final bool permitted;
  final Completer<void>? gate;
  final calls = <String>[];

  @override
  Future<bool> hasPermission({bool request = true}) async {
    await gate?.future;
    return permitted;
  }

  @override
  Future<void> start(RecordConfig config, {required String path}) async =>
      calls.add('start');
  @override
  Future<String?> stop() async {
    calls.add('stop');
    return null;
  }

  @override
  Future<void> cancel() async => calls.add('cancel');
  @override
  Future<void> dispose() async => calls.add('dispose');
}

class _FakeAndroid extends Fake implements SttAndroid {
  _FakeAndroid(this.endWith);

  /// The error code reported once a download is asked for, or null to report
  /// nothing, as a scheduled download does.
  final int? endWith;
  final downloads = <String>[];
  void Function(String language, int? errCode)? _onEnd;

  @override
  Future<void> downloadModel(String language) async {
    downloads.add(language);
    if (endWith case final err?) Timer.run(() => _onEnd?.call(language, err));
  }

  @override
  void onDownloadModelEnd(
    void Function(String language, int? errCode)? callback,
  ) =>
      _onEnd = callback;
}

class _FakeStt extends Fake implements Stt {
  _FakeStt({
    this.permitted = true,
    this.supported = true,
    this.fakeAndroid,
    this.gate,
  });

  final bool permitted;
  final bool supported;
  final _FakeAndroid? fakeAndroid;
  final Completer<void>? gate;
  final calls = <String>[];

  /// The native recognizer's language, shared like the one per process.
  static String language = 'en-US';

  // Sync, like the platform channels, which deliver in the order sent.
  final states = StreamController<SttState>.broadcast(sync: true);
  final results = StreamController<SttRecognition>.broadcast(sync: true);

  @override
  Future<bool> hasPermission() async => permitted;
  @override
  Future<bool> isSupported() async => supported;
  @override
  Future<String> getLanguage() async => language;
  @override
  Future<void> setLanguage(String language) async {
    calls.add('language $language');
    _FakeStt.language = language;
    await gate?.future;
  }

  @override
  Future<void> start([SttRecognitionOptions? options]) async =>
      calls.add('start');
  @override
  Future<void> stop() async => calls.add('stop');
  @override
  Future<void> dispose() async => calls.add('dispose');
  @override
  Stream<SttState> get onStateChanged => states.stream;
  @override
  Stream<SttRecognition> get onResultChanged => results.stream;
  @override
  SttAndroid? get android => fakeAndroid;
}

void main() {
  TestWidgetsFlutterBinding.ensureInitialized();

  setUp(() {
    _FakeStt.language = 'en-US';
    SystemEngine.resetDeviceLanguage();
  });

  group('OpenRouterEngine', () {
    OpenRouterEngine engine(_FakeRecorder recorder) => OpenRouterEngine(
      recorder: recorder,
      client: OpenRouterClient(),
      apiKey: 'sk-or-v1-abc',
      model: 'openai/whisper-1',
    );

    test('a refused microphone fails the start', () async {
      final e = engine(_FakeRecorder(permitted: false));
      await expectLater(e.start(), throwsA(isA<DictationDenied>()));
    });

    test('a missing recording fails the run', () async {
      final recorder = _FakeRecorder();
      final e = engine(recorder);
      final events = <DictationEvent>[];
      e.events.listen(events.add);

      await e.start();
      await e.stop();
      await pumpEventQueue();

      expect(events.single, isA<DictationFailed>());
      expect('${(events.single as DictationFailed).error}', 'Recording failed');
    });

    test('dispose cancels the recording', () async {
      final recorder = _FakeRecorder();
      final e = engine(recorder);
      await e.start();
      await e.dispose();

      expect(recorder.calls, ['start', 'cancel', 'dispose']);
    });

    test('dispose during the start does not start recording', () async {
      final gate = Completer<void>();
      final recorder = _FakeRecorder(gate: gate);
      final e = engine(recorder);
      final starting = e.start();
      await e.dispose();
      gate.complete();
      await starting;

      expect(recorder.calls, ['cancel', 'dispose']);
    });
  });

  group('SystemEngine', () {
    Future<(SystemEngine, _FakeStt, List<DictationEvent>)> started({
      String language = '',
    }) async {
      final stt = _FakeStt();
      final e = SystemEngine(stt, language: language);
      final events = <DictationEvent>[];
      e.events.listen(events.add);
      await e.start();
      return (e, stt, events);
    }

    test('a refused permission fails the start', () async {
      final e = SystemEngine(_FakeStt(permitted: false));
      await expectLater(e.start(), throwsA(isA<DictationDenied>()));
    });

    test(
      'an empty language resets the recognizer to the device language',
      () async {
        final (_, stt, _) = await started();
        expect(stt.calls, ['language en-US', 'start']);
      },
    );

    test('a picked language is set before the start', () async {
      final (_, stt, _) = await started(language: 'bn-BD');
      expect(stt.calls, ['language bn-BD', 'start']);
    });

    test('the device language survives a picked language', () async {
      await started(language: 'bn-BD');
      final (_, stt, _) = await started();
      expect(stt.calls, ['language en-US', 'start']);
      expect(await SpeechPacks(_FakeStt.new).resolve(''), 'en-US');
    });

    test('no recognizer fails the start', () async {
      final e = SystemEngine(_FakeStt(supported: false));
      await expectLater(
        e.start(),
        throwsA(isA<NoRecognizer>()),
      );
    });

    test('dispose during the start does not start the recognizer', () async {
      final gate = Completer<void>();
      final stt = _FakeStt(gate: gate);
      final e = SystemEngine(stt);
      final starting = e.start();
      await pumpEventQueue();
      await e.dispose();
      gate.complete();
      await starting;

      expect(stt.calls, ['language en-US', 'dispose']);
    });

    // testWidgets for its fake clock.
    testWidgets('a stop with no stop event still ends the run',
        (tester) async {
      final (e, _, events) = await started();
      await e.stop();
      await tester.pump(const Duration(seconds: 3));

      expect(events.single, isA<DictationDone>());
    });

    test('streams results and ends on the stop event', () async {
      final (_, stt, events) = await started();

      stt.states.add(SttState.start);
      stt.results.add(const SttRecognition('hello', false));
      stt.results.add(const SttRecognition('hello world', true));
      stt.states.add(SttState.stop);
      await pumpEventQueue();

      expect(events.map((e) => e is DictationText ? e.text : e.runtimeType), [
        'hello',
        'hello world',
        DictationDone,
      ]);
    });

    test(
      'ignores an earlier run\'s events that arrive before its start',
      () async {
        final (_, stt, events) = await started();

        stt.results.add(const SttRecognition('stale', true));
        stt.states.add(SttState.stop);
        await pumpEventQueue();
        expect(events, isEmpty);

        stt.states.add(SttState.start);
        stt.states.add(SttState.stop);
        await pumpEventQueue();
        expect(events.single, isA<DictationDone>());
      },
    );

    test('a stop before the start event still ends the run', () async {
      final (e, stt, events) = await started();

      await e.stop();
      stt.states.add(SttState.stop);
      await pumpEventQueue();

      expect(events.single, isA<DictationDone>());
    });

    test('an error fails the run, even before the start event', () async {
      final (_, stt, events) = await started();

      stt.states.addError('language_unavailable');
      stt.states.add(SttState.stop);
      await pumpEventQueue();

      expect(events.single, isA<DictationFailed>());
    });
  });

  group('SystemEngine errors', () {
    Future<DictationEvent> failWith(
      PlatformException e, {
      String? language,
    }) async {
      final stt = _FakeStt();
      final engine = SystemEngine(stt, language: language ?? '');
      final events = <DictationEvent>[];
      engine.events.listen(events.add);
      await engine.start();
      stt.states.addError(e);
      await pumpEventQueue();
      return events.single;
    }

    test('a missing Android language pack names the language', () async {
      final e = await failWith(
        PlatformException(code: '13', message: 'language_unavailable'),
        language: 'bn-BD',
      );
      final error = (e as DictationFailed).error;
      expect(error, isA<SpeechPackMissing>());
      expect((error as SpeechPackMissing).language, 'bn-BD');
    });

    test('an unsupported language is not a missing pack', () async {
      final e = await failWith(
        PlatformException(code: '12', message: 'language_not_supported'),
        language: 'bn-BD',
      );
      expect('${(e as DictationFailed).error}',
          'This recognizer does not support bn-BD');
    });

    test('the device language is named when none is picked', () async {
      final e = await failWith(PlatformException(code: '13'));
      expect(((e as DictationFailed).error as SpeechPackMissing).language,
          'en-US');
    });

    test('other errors read as text, not as a PlatformException', () async {
      Future<String> text(PlatformException p) async =>
          '${((await failWith(p)) as DictationFailed).error}';

      expect(await text(PlatformException(code: '6')), 'No speech detected');
      expect(
        await text(PlatformException(
            code: 'stt', message: 'Failed to initialize recognizer')),
        'Failed to initialize recognizer',
      );
      expect(await text(PlatformException(code: '5')),
          'Speech recognition failed (5)');
    });
  });

  group('SpeechPacks', () {
    // testWidgets for its fake clock: the wait is two real seconds otherwise.
    testWidgets('a download that reports nothing counts as started',
        (tester) async {
      final android = _FakeAndroid(null);
      PackDownload? result;
      SpeechPacks(() => _FakeStt(fakeAndroid: android))
          .download('bn-BD')
          .then((r) => result = r);
      await tester.pump(const Duration(seconds: 3));
      expect(result, PackDownload.started);
      expect(android.downloads, ['bn-BD']);
    });

    test('cannot_check_support means the device cannot download', () async {
      final packs =
          SpeechPacks(() => _FakeStt(fakeAndroid: _FakeAndroid(14)));
      expect(await packs.download('en-US'), PackDownload.unsupported);
    });

    test('another error code is a failed download', () async {
      final packs = SpeechPacks(() => _FakeStt(fakeAndroid: _FakeAndroid(5)));
      expect(await packs.download('en-US'), PackDownload.failed);
    });

    test('no Android API means the device cannot download', () async {
      expect(await SpeechPacks(_FakeStt.new).download('en-US'),
          PackDownload.unsupported);
    });

    group('isInstalled', () {
      const channel = MethodChannel('dev.muniftanjim.argus/speech');
      final messenger =
          TestDefaultBinaryMessengerBinding.instance.defaultBinaryMessenger;
      tearDown(() => messenger.setMockMethodCallHandler(channel, null));

      void reply(Object? installed) =>
          messenger.setMockMethodCallHandler(channel, (call) async {
            expect(call.method, 'installedLanguages');
            return installed;
          });

      test('matches tags regardless of case and separator', () async {
        reply(['en_US', 'bn-BD']);
        final packs = SpeechPacks(_FakeStt.new);
        expect(await packs.isInstalled('en-us'), isTrue);
        expect(await packs.isInstalled('bn-BD'), isTrue);
        expect(await packs.isInstalled('de-DE'), isFalse);
      });

      test('is null when the device cannot tell', () async {
        reply(null);
        expect(await SpeechPacks(_FakeStt.new).isInstalled('en-US'), isNull);
      });
    });

    test('resolves an empty selection to the device language', () async {
      final packs = SpeechPacks(_FakeStt.new);
      expect(await packs.resolve(''), 'en-US');
      expect(await packs.resolve('bn-BD'), 'bn-BD');
    });
  });
}
