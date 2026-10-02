import 'package:argus/data/dictation_engine.dart';
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

class _FakePacks extends Fake implements SpeechPacks {
  _FakePacks({this.available = true, this.installed = false});

  @override
  final bool available;
  bool? installed;
  final downloads = <String>[];
  final checked = <String>[];

  @override
  Future<bool?> isInstalled(String language) async {
    checked.add(language);
    return installed;
  }

  @override
  Future<String> resolve(String selected) async =>
      selected.isEmpty ? 'en-US' : selected;

  @override
  Future<PackDownload> download(String language) async {
    downloads.add(language);
    return PackDownload.started;
  }
}

Widget _app(
  VoiceStore store, {
  List<TranscriptionModel>? models,
  List<String> languages = const ['bn-BD', 'en-US'],
  _FakePacks? packs,
}) =>
    ProviderScope(
      overrides: [
        voiceStoreProvider.overrideWithValue(store),
        speechPacksProvider.overrideWithValue(packs ?? _FakePacks()),
        // Keep the screen off the platform channel.
        systemLanguagesProvider.overrideWith((ref) async => languages),
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
const _off = Key('voice-provider-off');
const _openrouter = Key('voice-provider-openrouter');
const _system = Key('voice-provider-system');
const _language = Key('system-language');

Future<VoiceStore> _openRouter(SecureKv kv) async {
  final store = VoiceStore(kv);
  await store.setProvider(VoiceProvider.openrouter);
  return store;
}

void main() {
  testWidgets('with no provider, only the provider choice shows',
      (tester) async {
    await tester.pumpWidget(_app(VoiceStore(_MemKv())));
    await tester.pumpAndSettle();

    expect(find.byKey(_off), findsOneWidget);
    expect(find.byKey(_openrouter), findsOneWidget);
    expect(find.byKey(_field), findsNothing);
    expect(find.byKey(const Key('transcription-model')), findsNothing);
    expect(find.text('Pick a provider to turn on the mic button.'),
        findsOneWidget);
  });

  testWidgets('picking OpenRouter persists it and shows its settings',
      (tester) async {
    final kv = _MemKv();
    await tester.pumpWidget(_app(VoiceStore(kv)));
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(_openrouter));
    await tester.pumpAndSettle();

    expect(await kv.read('voice.provider'), 'openrouter');
    expect(find.byKey(_field), findsOneWidget);
    expect(find.byKey(const Key('transcription-model')), findsOneWidget);
    await tester.scrollUntilVisible(
      find.text('Add a key to turn on the mic button.'),
      100,
      scrollable: find.byType(Scrollable).first,
    );
  });

  testWidgets('turning the provider off hides its settings and keeps the key',
      (tester) async {
    final kv = _MemKv();
    final store = await _openRouter(kv);
    await store.setApiKey('sk-or-v1-abc');
    await tester.pumpWidget(_app(store));
    await tester.pumpAndSettle();

    expect(find.byKey(_field), findsOneWidget);

    await tester.tap(find.byKey(_off));
    await tester.pumpAndSettle();

    expect(await kv.read('voice.provider'), 'off');
    expect(await kv.read('voice.openrouterApiKey'), 'sk-or-v1-abc');
    expect(find.byKey(_field), findsNothing);
    expect(find.text('Pick a provider to turn on the mic button.'),
        findsOneWidget);
  });

  testWidgets('typing alone does not persist; Save does', (tester) async {
    final kv = _MemKv();
    await tester.pumpWidget(_app(await _openRouter(kv)));
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
    final store = await _openRouter(kv);
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
    await tester.pumpWidget(_app(await _openRouter(kv)));
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
    await tester.pumpWidget(_app(await _openRouter(_MemKv()), models: many));
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
    final store = await _openRouter(kv);
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

  testWidgets('picking System turns the mic on with no key', (tester) async {
    final kv = _MemKv();
    await tester.pumpWidget(_app(VoiceStore(kv)));
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(_system));
    await tester.pumpAndSettle();

    expect(await kv.read('voice.provider'), 'system');
    expect(find.byKey(_field), findsNothing);
    expect(find.byKey(_language), findsOneWidget);
    expect(find.text('Device language'), findsOneWidget);
    expect(find.text('Add a key to turn on the mic button.'), findsNothing);
  });

  testWidgets('picking a language persists it', (tester) async {
    final kv = _MemKv();
    final store = VoiceStore(kv);
    await store.setProvider(VoiceProvider.system);
    await tester.pumpWidget(_app(store));
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(_language));
    await tester.pumpAndSettle();
    await tester.tap(find.text('bn-BD').last);
    await tester.pumpAndSettle();

    expect(await kv.read('voice.language'), 'bn-BD');
  });

  testWidgets('a stored language the recognizer no longer lists stays',
      (tester) async {
    final store = VoiceStore(_MemKv());
    await store.setProvider(VoiceProvider.system);
    await store.setLanguage('cy-GB');
    await tester.pumpWidget(_app(store));
    await tester.pumpAndSettle();

    expect(
      tester.widget<DropdownButton<String>>(find.byKey(_language)).value,
      'cy-GB',
    );
    expect(find.text('cy-GB'), findsOneWidget);
  });

  testWidgets('Download speech pack asks for the selected language',
      (tester) async {
    final packs = _FakePacks();
    final store = VoiceStore(_MemKv());
    await store.setProvider(VoiceProvider.system);
    await tester.pumpWidget(_app(store, packs: packs));
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('download-speech-pack')));
    await tester.pumpAndSettle();
    expect(packs.downloads, ['en-US']);
    // Checked again after the request.
    expect(packs.checked, ['en-US', 'en-US']);
    expect(
      find.text('Downloading the speech pack. Try again when it finishes.'),
      findsOneWidget,
    );
  });

  testWidgets('Download speech pack hides where packs cannot be installed',
      (tester) async {
    final store = VoiceStore(_MemKv());
    await store.setProvider(VoiceProvider.system);
    await tester.pumpWidget(_app(store, packs: _FakePacks(available: false)));
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('download-speech-pack')), findsNothing);
  });

  testWidgets('the tester shows only while the mic button is on',
      (tester) async {
    final kv = _MemKv();
    await tester.pumpWidget(_app(VoiceStore(kv)));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('voice-try-field')), findsNothing);

    await tester.tap(find.byKey(_system));
    await tester.pumpAndSettle();
    await tester.scrollUntilVisible(
      find.byKey(const Key('voice-try-clear')),
      100,
      scrollable: find.byType(Scrollable).first,
    );
    expect(find.byKey(const Key('voice-try-field')), findsOneWidget);
    expect(find.byKey(const Key('voice-input')), findsOneWidget);
    expect(
      find.text('A test dictation is billed like a real one.'),
      findsNothing,
    );
  });

  testWidgets('with OpenRouter, the tester warns that tests are billed',
      (tester) async {
    final store = await _openRouter(_MemKv());
    await store.setApiKey('sk-or-v1-abc');
    await tester.pumpWidget(_app(store));
    await tester.pumpAndSettle();

    final warning = find.text('A test dictation is billed like a real one.');
    await tester.scrollUntilVisible(
      warning,
      100,
      scrollable: find.byType(Scrollable).first,
    );
    expect(warning, findsOneWidget);
  });

  testWidgets('Clear empties the tester', (tester) async {
    final store = VoiceStore(_MemKv());
    await store.setProvider(VoiceProvider.system);
    await tester.pumpWidget(_app(store));
    await tester.pumpAndSettle();

    final field = find.byKey(const Key('voice-try-field'));
    await tester.scrollUntilVisible(
      find.byKey(const Key('voice-try-clear')),
      100,
      scrollable: find.byType(Scrollable).first,
    );
    await tester.enterText(field, 'hello');
    await tester.tap(find.byKey(const Key('voice-try-clear')));
    await tester.pump();

    expect(tester.widget<TextField>(field).controller!.text, isEmpty);
  });

  testWidgets('an installed pack replaces the download button',
      (tester) async {
    final packs = _FakePacks(installed: true);
    final store = VoiceStore(_MemKv());
    await store.setProvider(VoiceProvider.system);
    await store.setLanguage('bn-BD');
    await tester.pumpWidget(_app(store, packs: packs));
    await tester.pumpAndSettle();

    expect(packs.checked, ['bn-BD']);
    expect(find.byKey(const Key('speech-pack-installed')), findsOneWidget);
    expect(find.byKey(const Key('download-speech-pack')), findsNothing);
  });

  testWidgets('returning to the app checks the pack again', (tester) async {
    final packs = _FakePacks();
    final store = VoiceStore(_MemKv());
    await store.setProvider(VoiceProvider.system);
    await tester.pumpWidget(_app(store, packs: packs));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('download-speech-pack')), findsOneWidget);

    packs.installed = true;
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.paused);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.hidden);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.inactive);
    tester.binding.handleAppLifecycleStateChanged(AppLifecycleState.resumed);
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('speech-pack-installed')), findsOneWidget);
  });

  testWidgets('a device that cannot tell shows neither', (tester) async {
    final store = VoiceStore(_MemKv());
    await store.setProvider(VoiceProvider.system);
    await tester.pumpWidget(_app(store, packs: _FakePacks()..installed = null));
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('speech-pack-installed')), findsNothing);
    expect(find.byKey(const Key('download-speech-pack')), findsNothing);
  });
}
