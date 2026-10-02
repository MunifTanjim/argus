import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:record/record.dart';
import 'package:stts/stts.dart';

import '../data/dictation_engine.dart';
import '../data/openrouter.dart';
import '../pairing/gateway_store.dart';

/// Fast, cheap and widely available. The user can pick another in settings.
const defaultTranscriptionModel = 'openai/whisper-large-v3-turbo';

enum VoiceProvider {
  off('Off'),
  openrouter('OpenRouter'),
  system('System');

  const VoiceProvider(this.label);
  final String label;

  static VoiceProvider parse(String? name) => values.firstWhere(
        (p) => p.name == name,
        orElse: () => VoiceProvider.off,
      );
}

/// Persisted voice-input settings: the transcription provider, the user's own
/// OpenRouter key and the speech-to-text model to spend it on, and the System
/// recognizer's language.
class VoicePrefs {
  const VoicePrefs({
    this.provider = VoiceProvider.off,
    this.apiKey = '',
    this.model = defaultTranscriptionModel,
    this.language = '',
  });

  final VoiceProvider provider;
  final String apiKey;
  final String model;

  /// A recognizer language tag, or empty for the device language.
  final String language;

  /// The mic button stays hidden until a provider is picked, and for
  /// OpenRouter, until it has a key.
  bool get enabled => switch (provider) {
        VoiceProvider.off => false,
        VoiceProvider.openrouter => apiKey.isNotEmpty,
        VoiceProvider.system => true,
      };

  VoicePrefs copyWith({
    VoiceProvider? provider,
    String? apiKey,
    String? model,
    String? language,
  }) =>
      VoicePrefs(
        provider: provider ?? this.provider,
        apiKey: apiKey ?? this.apiKey,
        model: model ?? this.model,
        language: language ?? this.language,
      );
}

/// Reads/writes voice prefs through the app's secure KV. The key is a
/// credential, so it goes to the same store as the gateway token, never to
/// plain preferences.
class VoiceStore {
  VoiceStore([this._kv = const FlutterSecureKv()]);
  final SecureKv _kv;

  static const _providerKey = 'voice.provider';
  static const _apiKeyKey = 'voice.openrouterApiKey';
  static const _modelKey = 'voice.model';
  static const _languageKey = 'voice.language';

  Future<VoicePrefs> load() async {
    final provider = await _kv.read(_providerKey);
    final apiKey = await _kv.read(_apiKeyKey);
    final model = await _kv.read(_modelKey);
    final language = await _kv.read(_languageKey);
    return VoicePrefs(
      provider: VoiceProvider.parse(provider),
      apiKey: apiKey ?? '',
      model: (model == null || model.isEmpty) ? defaultTranscriptionModel : model,
      language: language ?? '',
    );
  }

  Future<void> setProvider(VoiceProvider v) => _kv.write(_providerKey, v.name);

  /// Deletes the record for an empty key so clearing it leaves nothing behind.
  Future<void> setApiKey(String v) =>
      v.isEmpty ? _kv.delete(_apiKeyKey) : _kv.write(_apiKeyKey, v);

  Future<void> setModel(String v) => _kv.write(_modelKey, v);

  Future<void> setLanguage(String v) =>
      v.isEmpty ? _kv.delete(_languageKey) : _kv.write(_languageKey, v);
}

final voiceStoreProvider = Provider<VoiceStore>((ref) => VoiceStore());

final openRouterClientProvider =
    Provider<OpenRouterClient>((ref) => OpenRouterClient());

final audioRecorderProvider =
    Provider<AudioRecorder Function()>((ref) => AudioRecorder.new);

final sttProvider = Provider<Stt Function()>((ref) => Stt.new);

/// Builds the engine for one dictation run from the current prefs.
final dictationEngineProvider = Provider<DictationEngine Function()>((ref) {
  return () {
    final prefs = ref.read(voicePrefsProvider);
    return switch (prefs.provider) {
      VoiceProvider.system =>
        SystemEngine(ref.read(sttProvider)(), language: prefs.language),
      _ => OpenRouterEngine(
          recorder: ref.read(audioRecorderProvider)(),
          client: ref.read(openRouterClientProvider),
          apiKey: prefs.apiKey,
          model: prefs.model,
        ),
    };
  };
});

final speechPacksProvider =
    Provider<SpeechPacks>((ref) => SpeechPacks(ref.read(sttProvider)));

/// Whether the pack for a selected language, or for the device language when
/// empty, is installed. Null when the device cannot tell.
final speechPackInstalledProvider =
    FutureProvider.autoDispose.family<bool?, String>((ref, selected) async {
  final packs = ref.read(speechPacksProvider);
  if (!packs.available) return null;
  return packs.isInstalled(await packs.resolve(selected));
});

/// The System recognizer's languages for the settings picker, sorted. The
/// [Stt] is not disposed: disposing stops any live recognition.
final systemLanguagesProvider = FutureProvider<List<String>>((ref) async {
  return (await ref.read(sttProvider)().getLanguages())..sort();
});

/// The speech-to-text catalog for the settings picker. Needs no key, so it
/// loads even before the user pastes one.
final transcriptionModelsProvider =
    FutureProvider<List<TranscriptionModel>>((ref) async {
  return ref.read(openRouterClientProvider).transcriptionModels();
});

class VoiceController extends Notifier<VoicePrefs> {
  @override
  VoicePrefs build() {
    // Hydrate async; a one-frame default before storage loads is acceptable,
    // and it only hides the mic button for that frame.
    _load();
    return const VoicePrefs();
  }

  Future<void> _load() async {
    try {
      state = await ref.read(voiceStoreProvider).load();
    } catch (_) {
      // Keep the default on read failure (e.g. secure storage unavailable).
    }
  }

  Future<void> setProvider(VoiceProvider v) async {
    state = state.copyWith(provider: v);
    try {
      await ref.read(voiceStoreProvider).setProvider(v);
    } catch (_) {
      // Persist failure is non-fatal.
    }
  }

  Future<void> setApiKey(String v) async {
    state = state.copyWith(apiKey: v);
    try {
      await ref.read(voiceStoreProvider).setApiKey(v);
    } catch (_) {
      // Persist failure is non-fatal; the key survives until restart.
    }
  }

  Future<void> setModel(String v) async {
    state = state.copyWith(model: v);
    try {
      await ref.read(voiceStoreProvider).setModel(v);
    } catch (_) {
      // Persist failure is non-fatal.
    }
  }

  Future<void> setLanguage(String v) async {
    state = state.copyWith(language: v);
    try {
      await ref.read(voiceStoreProvider).setLanguage(v);
    } catch (_) {
      // Persist failure is non-fatal.
    }
  }
}

final voicePrefsProvider =
    NotifierProvider<VoiceController, VoicePrefs>(VoiceController.new);
