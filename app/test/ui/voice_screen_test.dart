import 'package:argus/data/openrouter.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/voice.dart';
import 'package:argus/ui/voice_screen.dart';
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

Widget _app(VoiceStore store, {List<TranscriptionModel>? models}) =>
    ProviderScope(
      overrides: [
        voiceStoreProvider.overrideWithValue(store),
        // Keep the screen off the network.
        transcriptionModelsProvider.overrideWith(
          (ref) async =>
              models ??
              const [TranscriptionModel('openai/whisper-1', 'Whisper')],
        ),
      ],
      child: const MaterialApp(home: VoiceScreen()),
    );

const _field = Key('openrouter-api-key');
const _saveButton = Key('save-api-key');

void main() {
  testWidgets('typing alone does not persist; Save does', (tester) async {
    final kv = _MemKv();
    await tester.pumpWidget(_app(VoiceStore(kv)));
    await tester.pumpAndSettle();

    expect(find.text('No key saved'), findsOneWidget);

    await tester.enterText(find.byKey(_field), 'sk-or-v1-abc');
    await tester.pump();

    // Still unsaved: the store must not have been touched by the keystrokes.
    expect(find.text('Unsaved changes'), findsOneWidget);
    expect(await kv.read('voice.openrouterApiKey'), isNull);

    await tester.tap(find.byKey(_saveButton));
    await tester.pumpAndSettle();

    expect(await kv.read('voice.openrouterApiKey'), 'sk-or-v1-abc');
    expect(find.text('API key saved'), findsOneWidget); // the snackbar
    expect(find.text('Key saved'), findsOneWidget);
  });

  testWidgets('Save is disabled until the field differs from what is stored',
      (tester) async {
    final kv = _MemKv();
    final store = VoiceStore(kv);
    await store.setApiKey('sk-or-v1-abc');
    await tester.pumpWidget(_app(store));
    await tester.pumpAndSettle();

    FilledButton save() => tester.widget<FilledButton>(find.byKey(_saveButton));
    expect(save().onPressed, isNull);
    expect(find.text('Key saved'), findsOneWidget);

    await tester.enterText(find.byKey(_field), 'sk-or-v1-xyz');
    await tester.pump();
    expect(save().onPressed, isNotNull);
  });

  testWidgets('a pasted key is trimmed before it is stored', (tester) async {
    final kv = _MemKv();
    await tester.pumpWidget(_app(VoiceStore(kv)));
    await tester.pumpAndSettle();

    await tester.enterText(find.byKey(_field), '  sk-or-v1-abc\n');
    await tester.pump();
    await tester.tap(find.byKey(_saveButton));
    await tester.pumpAndSettle();

    expect(await kv.read('voice.openrouterApiKey'), 'sk-or-v1-abc');
  });

  testWidgets('the model menu is capped so a long catalog cannot fill the screen',
      (tester) async {
    // The real catalog is ~22 models; an uncapped menu is taller than a phone.
    final many = [
      for (var i = 0; i < 22; i++)
        TranscriptionModel('vendor/model-$i', 'Model $i',
            perAudioSecond: i * 0.000001),
    ];
    await tester.pumpWidget(_app(VoiceStore(_MemKv()), models: many));
    await tester.pumpAndSettle();

    final menu = tester.widget<DropdownButton<String>>(
      find.byKey(const Key('transcription-model')),
    );
    expect(menu.menuMaxHeight, isNotNull);
    // Seven rows at the 48px minimum touch target, no more.
    expect(menu.menuMaxHeight, lessThanOrEqualTo(7 * 48.0));
  });

  testWidgets('clearing the field and saving removes the stored key',
      (tester) async {
    final kv = _MemKv();
    final store = VoiceStore(kv);
    await store.setApiKey('sk-or-v1-abc');
    await tester.pumpWidget(_app(store));
    await tester.pumpAndSettle();

    await tester.enterText(find.byKey(_field), '');
    await tester.pump();
    expect(find.text('Save will remove the stored key'), findsOneWidget);

    await tester.tap(find.byKey(_saveButton));
    await tester.pumpAndSettle();

    expect(await kv.read('voice.openrouterApiKey'), isNull);
    expect(find.text('API key removed'), findsOneWidget);
  });
}
