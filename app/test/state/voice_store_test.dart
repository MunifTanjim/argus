import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/voice.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeKv implements SecureKv {
  final Map<String, String> _m = {};

  @override
  Future<String?> read(String key) async => _m[key];
  @override
  Future<void> write(String key, String value) async => _m[key] = value;
  @override
  Future<void> delete(String key) async => _m.remove(key);
}

void main() {
  test('defaults to no key and the default model', () async {
    final prefs = await VoiceStore(_FakeKv()).load();
    expect(prefs.apiKey, isEmpty);
    expect(prefs.model, defaultTranscriptionModel);
    expect(prefs.enabled, isFalse);
  });

  test('persists and reloads the key and model', () async {
    final kv = _FakeKv();
    await VoiceStore(kv).setApiKey('sk-or-v1-abc');
    await VoiceStore(kv).setModel('google/chirp-3');

    final prefs = await VoiceStore(kv).load();
    expect(prefs.apiKey, 'sk-or-v1-abc');
    expect(prefs.model, 'google/chirp-3');
    expect(prefs.enabled, isTrue);
  });

  test('clearing the key deletes the record and disables voice input', () async {
    final kv = _FakeKv();
    await VoiceStore(kv).setApiKey('sk-or-v1-abc');
    await VoiceStore(kv).setApiKey('');

    expect(await kv.read('voice.openrouterApiKey'), isNull);
    expect((await VoiceStore(kv).load()).enabled, isFalse);
  });

  test('an empty stored model falls back to the default', () async {
    final kv = _FakeKv();
    await kv.write('voice.model', '');
    expect((await VoiceStore(kv).load()).model, defaultTranscriptionModel);
  });
}
