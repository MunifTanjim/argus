import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/voice.dart';
import 'package:argus/ui/voice_input_field.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:record/record.dart';

class _MemKv implements SecureKv {
  final _m = <String, String>{};
  @override
  Future<String?> read(String key) async => _m[key];
  @override
  Future<void> write(String key, String value) async => _m[key] = value;
  @override
  Future<void> delete(String key) async => _m.remove(key);
}

class _FakeRecorder extends Fake implements AudioRecorder {
  _FakeRecorder({this.permitted = true});

  final bool permitted;
  final calls = <String>[];

  @override
  Future<bool> hasPermission({bool request = true}) async => permitted;
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
  Future<void> dispose() async {}
  @override
  Stream<Amplitude> onAmplitudeChanged(Duration interval) =>
      const Stream.empty();
}

/// Renders the field the way the reply sheet does.
Widget _host(
  VoiceStore store,
  TextEditingController c, {
  AudioRecorder? recorder,
  bool statusBelow = false,
}) =>
    ProviderScope(
      overrides: [
        voiceStoreProvider.overrideWithValue(store),
        audioRecorderProvider.overrideWithValue(() => recorder ?? _FakeRecorder()),
      ],
      child: MaterialApp(
        home: Scaffold(
          body: VoiceInputField(
            controller: c,
            statusBelow: statusBelow,
            field: (suffixIcon) => TextField(
              key: _field,
              controller: c,
              minLines: 1,
              maxLines: 6,
              decoration: InputDecoration(
                border: const OutlineInputBorder(),
                suffixIcon: suffixIcon,
              ),
            ),
          ),
        ),
      ),
    );

const _field = Key('field');
const _mic = Key('voice-input');

Future<VoiceStore> _enabled() async {
  final store = VoiceStore(_MemKv());
  await store.setProvider(VoiceProvider.openrouter);
  await store.setApiKey('sk-or-v1-abc');
  return store;
}

TextEditingValue _at(String text, int offset) => TextEditingValue(
      text: text,
      selection: TextSelection.collapsed(offset: offset),
    );

void main() {
  group('insertAtCursor', () {
    test('appends to an untouched field, which reports offset -1', () {
      final v = insertAtCursor(const TextEditingValue(text: 'hi'), 'there');
      expect(v.text, 'hi there');
      expect(v.selection.baseOffset, 8);
    });

    test('inserts at the cursor, not at the end', () {
      final v = insertAtCursor(_at('ab cd', 3), 'XY');
      expect(v.text, 'ab XY cd');
      expect(v.selection.baseOffset, 5);
    });

    test('replaces the selection', () {
      const sel = TextEditingValue(
        text: 'keep drop keep',
        selection: TextSelection(baseOffset: 5, extentOffset: 9),
      );
      final v = insertAtCursor(sel, 'new');
      expect(v.text, 'keep new keep');
      expect(v.selection.baseOffset, 8);
    });

    test('pads only where it would run into a neighbouring word', () {
      expect(insertAtCursor(_at('one', 3), 'two').text, 'one two');
      expect(insertAtCursor(_at('one ', 4), 'two').text, 'one two');
      expect(insertAtCursor(_at('one\n', 4), 'two').text, 'one\ntwo');
      expect(insertAtCursor(_at('', 0), 'two').text, 'two');
      expect(insertAtCursor(_at('one two', 0), 'zero').text, 'zero one two');
      expect(insertAtCursor(_at(' two', 0), 'one').text, 'one two');
    });
  });

  group('the key gate', () {
    testWidgets('no key means the bare field, with no space reserved',
        (tester) async {
      final c = TextEditingController();
      addTearDown(c.dispose);
      await tester.pumpWidget(_host(VoiceStore(_MemKv()), c));
      await tester.pumpAndSettle();

      expect(find.byKey(_field), findsOneWidget);
      expect(find.byKey(_mic), findsNothing);
      expect(
        tester.widget<TextField>(find.byKey(_field)).decoration!.suffixIcon,
        isNull,
      );
    });

    testWidgets('a provider with a saved key brings the mic back',
        (tester) async {
      final c = TextEditingController();
      addTearDown(c.dispose);
      await tester.pumpWidget(_host(await _enabled(), c));
      await tester.pumpAndSettle();

      expect(find.byKey(_mic), findsOneWidget);
      expect(
        tester.widget<TextField>(find.byKey(_field)).decoration!.suffixIcon,
        isNotNull,
      );
    });

    testWidgets('idle shows only the mic, in the bottom-right corner',
        (tester) async {
      final c = TextEditingController(text: 'one\ntwo\nthree');
      addTearDown(c.dispose);
      await tester.pumpWidget(_host(await _enabled(), c));
      await tester.pumpAndSettle();

      // No label and no status row until there is something to show.
      expect(find.byKey(const Key('voice-cancel')), findsNothing);
      expect(find.text('Dictate'), findsNothing);
      final mic = tester.getRect(find.byKey(_mic));
      final field = tester.getRect(find.byKey(_field));
      expect(mic.right, closeTo(field.right, 0.5));
    });

    for (final text in ['one', 'one\ntwo\nthree']) {
      testWidgets(
          'the mic is centered on the last line of '
          '${text.split('\n').length} line(s)', (tester) async {
        final c = TextEditingController(text: text);
        addTearDown(c.dispose);
        await tester.pumpWidget(_host(await _enabled(), c));
        await tester.pumpAndSettle();

        final editable = tester
            .state<EditableTextState>(find.byType(EditableText))
            .renderEditable;
        final lastLineBottom = editable
            .localToGlobal(Offset(0, editable.size.height))
            .dy;
        final icon = tester.getRect(find.descendant(
          of: find.byKey(_mic),
          matching: find.byType(Icon),
        ));
        expect(
          icon.center.dy,
          closeTo(lastLineBottom - editable.preferredLineHeight / 2, 0.5),
        );
      });
    }
  });

  group('hold to record', () {
    Future<_FakeRecorder> pumpHost(
      WidgetTester tester, {
      bool statusBelow = false,
    }) async {
      final recorder = _FakeRecorder();
      final c = TextEditingController();
      addTearDown(c.dispose);
      await tester.pumpWidget(_host(
        await _enabled(),
        c,
        recorder: recorder,
        statusBelow: statusBelow,
      ));
      await tester.pumpAndSettle();
      return recorder;
    }

    testWidgets('a hold of at least a second sends the clip', (tester) async {
      final recorder = await pumpHost(tester);

      final hold = await tester.startGesture(tester.getCenter(find.byKey(_mic)));
      await tester.pump();
      expect(recorder.calls, ['start']);
      expect(find.text('Slide to cancel'), findsOneWidget);
      expect(find.byIcon(Icons.arrow_back), findsOneWidget);
      // Swipe replaces the trash can while a finger is on the mic.
      expect(find.byKey(const Key('voice-cancel')), findsNothing);

      await tester.pump(const Duration(milliseconds: 1100));
      await hold.up();
      await tester.pumpAndSettle();

      expect(recorder.calls, ['start', 'stop']);
    });

    testWidgets('a shorter hold discards the clip and says to hold',
        (tester) async {
      final recorder = await pumpHost(tester);

      await tester.tap(find.byKey(_mic));
      await tester.pumpAndSettle();

      expect(recorder.calls, ['start', 'cancel']);
      expect(find.text('Hold to record'), findsOneWidget);
    });

    testWidgets('sliding left past the threshold discards on release',
        (tester) async {
      final recorder = await pumpHost(tester);

      final hold = await tester.startGesture(tester.getCenter(find.byKey(_mic)));
      await tester.pump(const Duration(milliseconds: 1100));
      // The hint lights up as the finger slides, before cancel is armed.
      double lit() => (tester.widget(find.byKey(const Key('voice-slide-hint')))
              as dynamic)
          .progress as double;
      expect(lit(), 0);
      await hold.moveBy(const Offset(-40, 0));
      await tester.pump();
      expect(lit(), closeTo(0.5, 0.01));
      await hold.moveBy(const Offset(40, 0));
      await tester.pump();
      expect(lit(), 0);

      await hold.moveBy(const Offset(-100, 0));
      await tester.pump();
      expect(find.text('Release to cancel'), findsOneWidget);

      await hold.up();
      await tester.pumpAndSettle();

      expect(recorder.calls, ['start', 'cancel']);
      expect(find.byKey(const Key('voice-cancel')), findsNothing);
    });

    testWidgets('sliding back before release keeps the clip', (tester) async {
      final recorder = await pumpHost(tester);

      final hold = await tester.startGesture(tester.getCenter(find.byKey(_mic)));
      await tester.pump(const Duration(milliseconds: 1100));
      await hold.moveBy(const Offset(-100, 0));
      await tester.pump();
      await hold.moveBy(const Offset(60, 0));
      await tester.pump();
      expect(find.text('Slide to cancel'), findsOneWidget);

      await hold.up();
      await tester.pumpAndSettle();

      expect(recorder.calls, ['start', 'stop']);
    });

    testWidgets('sliding up locks, and a tap on stop sends the clip',
        (tester) async {
      final recorder = await pumpHost(tester);

      final hold = await tester.startGesture(tester.getCenter(find.byKey(_mic)));
      await tester.pump();
      expect(find.byKey(const Key('voice-lock-hint')), findsOneWidget);

      // The hint fills as the finger rises, before the lock engages.
      double fill() => tester
          .widget<FractionallySizedBox>(find.byKey(const Key('voice-lock-fill')))
          .heightFactor!;
      expect(fill(), 0);
      await hold.moveBy(const Offset(0, -40));
      await tester.pump();
      expect(fill(), closeTo(0.5, 0.01));

      await hold.moveBy(const Offset(0, -60));
      await tester.pump();
      expect(find.byKey(const Key('voice-lock-hint')), findsNothing);
      expect(find.text('Slide to cancel'), findsNothing);
      // Hands-free now: the trash can is back and the mic is a stop button.
      expect(find.byKey(const Key('voice-cancel')), findsOneWidget);
      expect(find.byIcon(Icons.stop_circle), findsOneWidget);

      // Lifting the finger neither stops nor discards. A lock shows intent, so
      // the one-second minimum does not apply.
      await hold.up();
      await tester.pump();
      expect(recorder.calls, ['start']);

      await tester.tap(find.byKey(_mic));
      await tester.pumpAndSettle();
      expect(recorder.calls, ['start', 'stop']);
    });

    testWidgets('with the status row below, the lock is below too',
        (tester) async {
      final recorder = await pumpHost(tester, statusBelow: true);

      final hold = await tester.startGesture(tester.getCenter(find.byKey(_mic)));
      await tester.pump();
      final mic = tester.getRect(find.byKey(_mic));
      final hint = tester.getRect(find.byKey(const Key('voice-lock-hint')));
      expect(hint.top, greaterThanOrEqualTo(mic.bottom));
      expect(find.byIcon(Icons.keyboard_arrow_down), findsOneWidget);

      // Up does nothing here; down locks.
      await hold.moveBy(const Offset(0, -100));
      await tester.pump();
      expect(find.byKey(const Key('voice-lock-hint')), findsOneWidget);
      await hold.moveBy(const Offset(0, 200));
      await tester.pump();
      expect(find.byKey(const Key('voice-lock-hint')), findsNothing);
      expect(find.byIcon(Icons.stop_circle), findsOneWidget);

      await hold.up();
      await tester.pump();
      expect(recorder.calls, ['start']);
    });

    testWidgets('once cancel is armed, sliding up does not lock',
        (tester) async {
      final recorder = await pumpHost(tester);

      final hold = await tester.startGesture(tester.getCenter(find.byKey(_mic)));
      await tester.pump();
      await hold.moveBy(const Offset(-100, 0));
      await tester.pump();
      await hold.moveBy(const Offset(0, -100));
      await tester.pump();
      expect(find.text('Release to cancel'), findsOneWidget);

      await hold.up();
      await tester.pumpAndSettle();
      expect(recorder.calls, ['start', 'cancel']);
    });

    testWidgets('a screen reader tap toggles recording', (tester) async {
      final recorder = await pumpHost(tester);
      void semanticTap() =>
          tester.widget<Semantics>(find.byKey(_mic)).properties.onTap!();

      semanticTap();
      await tester.pumpAndSettle();
      expect(recorder.calls, ['start']);
      // No finger to slide, so the trash can stays available.
      expect(find.byKey(const Key('voice-cancel')), findsOneWidget);

      semanticTap();
      await tester.pumpAndSettle();
      expect(recorder.calls, ['start', 'stop']);
    });
  });

  group('errors', () {
    Future<void> pumpDenied(WidgetTester tester) async {
      final c = TextEditingController();
      addTearDown(c.dispose);
      await tester.pumpWidget(
        _host(await _enabled(), c, recorder: _FakeRecorder(permitted: false)),
      );
      await tester.pumpAndSettle();
      await tester.tap(find.byKey(_mic));
      await tester.pumpAndSettle();
    }

    testWidgets('show in the status row, not in a snackbar', (tester) async {
      await pumpDenied(tester);

      expect(find.byType(SnackBar), findsNothing);
      final error = tester.widget<Text>(find.byKey(const Key('voice-message')));
      expect(error.data, 'Microphone permission denied');
      expect(find.byIcon(Icons.mic_none), findsOneWidget);
    });

    testWidgets('the trash can dismisses the error', (tester) async {
      await pumpDenied(tester);

      await tester.tap(find.byKey(const Key('voice-cancel')));
      await tester.pumpAndSettle();

      expect(find.byKey(const Key('voice-message')), findsNothing);
      expect(find.byKey(const Key('voice-cancel')), findsNothing);
    });
  });

  group('LevelMeter', () {
    // The spawn dialog is an AlertDialog, which sizes its content with
    // IntrinsicWidth.
    testWidgets('lays out inside IntrinsicWidth', (tester) async {
      await tester.pumpWidget(const MaterialApp(
        home: Scaffold(
          body: IntrinsicWidth(
            child: Row(children: [Expanded(child: LevelMeter(levels: [0.5]))]),
          ),
        ),
      ));
      expect(tester.takeException(), isNull);
    });

    testWidgets('fills the width it is given', (tester) async {
      await tester.pumpWidget(const MaterialApp(
        home: Scaffold(
          body: Center(
            child: SizedBox(width: 300, child: LevelMeter(levels: [0.5])),
          ),
        ),
      ));
      expect(tester.getSize(find.byType(LevelMeter)).width, 300);
    });
  });

  group('formatClock', () {
    test('reads as m:ss with a zero-padded seconds field', () {
      expect(formatClock(Duration.zero), '0:00');
      expect(formatClock(const Duration(seconds: 7)), '0:07');
      expect(formatClock(const Duration(seconds: 59)), '0:59');
      expect(formatClock(const Duration(seconds: 60)), '1:00');
      expect(formatClock(const Duration(minutes: 2, seconds: 5)), '2:05');
      expect(formatClock(const Duration(minutes: 12, seconds: 34)), '12:34');
    });
  });

  group('meterLevel', () {
    test('anchors silence at -45 dBFS and clips at 0', () {
      expect(meterLevel(0), 1.0);
      expect(meterLevel(-45), 0.0);
      expect(meterLevel(-22.5), closeTo(0.5, 0.001));
    });

    test('clamps readings outside the window', () {
      expect(meterLevel(-160), 0.0); // digital silence
      expect(meterLevel(5), 1.0); // over-driven
    });

    test('a non-finite reading is silence, not a crash', () {
      expect(meterLevel(double.negativeInfinity), 0.0);
      expect(meterLevel(double.nan), 0.0);
    });
  });
}
