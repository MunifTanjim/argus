import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/voice.dart';
import 'package:argus/ui/voice_input_bar.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _MemKv implements SecureKv {
  final _m = <String, String>{};
  @override
  Future<String?> read(String key) async => _m[key];
  @override
  Future<void> write(String key, String value) async => _m[key] = value;
  @override
  Future<void> delete(String key) async => _m.remove(key);
}

/// Renders the bar exactly as the reply and spawn fields do.
Widget _host(VoiceStore store, TextEditingController c) => ProviderScope(
      overrides: [voiceStoreProvider.overrideWithValue(store)],
      child: MaterialApp(
        home: Scaffold(
          body: Consumer(
            builder: (_, ref, _) => Column(children: [?voiceInputBar(ref, c)]),
          ),
        ),
      ),
    );

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
    testWidgets('no key means no bar at all, not an empty row',
        (tester) async {
      final c = TextEditingController();
      addTearDown(c.dispose);
      await tester.pumpWidget(_host(VoiceStore(_MemKv()), c));
      await tester.pumpAndSettle();

      expect(find.byType(VoiceInputBar), findsNothing);
      expect(find.byKey(const Key('voice-input')), findsNothing);
      // Nothing reserved either. The null-aware element drops the slot instead
      // of leaving a placeholder, so users without a key see no gap.
      expect(tester.widget<Column>(find.byType(Column)).children, isEmpty);
    });

    testWidgets('a saved key brings the bar back', (tester) async {
      final kv = _MemKv();
      final store = VoiceStore(kv);
      await store.setApiKey('sk-or-v1-abc');
      final c = TextEditingController();
      addTearDown(c.dispose);

      await tester.pumpWidget(_host(store, c));
      await tester.pumpAndSettle();

      expect(find.byKey(const Key('voice-input')), findsOneWidget);
      expect(find.text('Dictate'), findsOneWidget);
    });

    testWidgets('idle shows the mic on the right and no cancel',
        (tester) async {
      final store = VoiceStore(_MemKv());
      await store.setApiKey('sk-or-v1-abc');
      final c = TextEditingController();
      addTearDown(c.dispose);

      await tester.pumpWidget(_host(store, c));
      await tester.pumpAndSettle();

      // Cancel only exists once there is something to discard.
      expect(find.byKey(const Key('voice-cancel')), findsNothing);
      // The mic is the rightmost thing in the bar: right-handed reach.
      final mic = tester.getRect(find.byKey(const Key('voice-input')));
      final bar = tester.getRect(find.byType(VoiceInputBar));
      expect(mic.right, closeTo(bar.right, 0.5));
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
