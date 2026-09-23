import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/openrouter.dart';
import '../pairing/gateway_store.dart';

/// Fast, cheap and widely available. The user can pick another in settings.
const defaultTranscriptionModel = 'openai/whisper-large-v3-turbo';

/// Persisted voice-input settings: the user's own OpenRouter key and the
/// speech-to-text model to spend it on.
class VoicePrefs {
  const VoicePrefs({this.apiKey = '', this.model = defaultTranscriptionModel});

  final String apiKey;
  final String model;

  /// The mic button stays hidden until there is a key to spend.
  bool get enabled => apiKey.isNotEmpty;

  VoicePrefs copyWith({String? apiKey, String? model}) =>
      VoicePrefs(apiKey: apiKey ?? this.apiKey, model: model ?? this.model);
}

/// Reads/writes voice prefs through the app's secure KV. The key is a
/// credential, so it goes to the same store as the gateway token, never to
/// plain preferences.
class VoiceStore {
  VoiceStore([this._kv = const FlutterSecureKv()]);
  final SecureKv _kv;

  static const _apiKeyKey = 'voice.openrouterApiKey';
  static const _modelKey = 'voice.model';

  Future<VoicePrefs> load() async {
    final apiKey = await _kv.read(_apiKeyKey);
    final model = await _kv.read(_modelKey);
    return VoicePrefs(
      apiKey: apiKey ?? '',
      model: (model == null || model.isEmpty) ? defaultTranscriptionModel : model,
    );
  }

  /// Deletes the record for an empty key so clearing it leaves nothing behind.
  Future<void> setApiKey(String v) =>
      v.isEmpty ? _kv.delete(_apiKeyKey) : _kv.write(_apiKeyKey, v);

  Future<void> setModel(String v) => _kv.write(_modelKey, v);
}

final voiceStoreProvider = Provider<VoiceStore>((ref) => VoiceStore());

final openRouterClientProvider =
    Provider<OpenRouterClient>((ref) => OpenRouterClient());

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
}

final voicePrefsProvider =
    NotifierProvider<VoiceController, VoicePrefs>(VoiceController.new);
