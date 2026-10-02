import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/openrouter.dart';
import '../state/voice.dart';
import 'responsive.dart';
import 'theme.dart';
import 'voice_input_field.dart';

/// Settings for dictation: the transcription provider, then that provider's
/// settings (for OpenRouter, the user's own key and the model it pays for; for
/// System, the recognizer's language).
class VoiceScreen extends ConsumerStatefulWidget {
  const VoiceScreen({super.key});

  @override
  ConsumerState<VoiceScreen> createState() => _VoiceScreenState();
}

class _VoiceScreenState extends ConsumerState<VoiceScreen> {
  late final TextEditingController _key;
  final _tryIt = TextEditingController();
  late final AppLifecycleListener _lifecycle;
  bool _reveal = false;

  @override
  void initState() {
    super.initState();
    _key = TextEditingController(text: ref.read(voicePrefsProvider).apiKey)
      ..addListener(_onEdit);
    // A pack download finishes in the background, so check again on return.
    _lifecycle = AppLifecycleListener(
      onResume: () => ref.invalidate(speechPackInstalledProvider),
    );
  }

  // Rebuild so the Save button and the status line follow what is typed.
  void _onEdit() => setState(() {});

  @override
  void dispose() {
    _lifecycle.dispose();
    _key.removeListener(_onEdit);
    _key.dispose();
    _tryIt.dispose();
    super.dispose();
  }

  Future<void> _save() async {
    // A pasted key usually carries a trailing newline or space, which would
    // otherwise go into the Authorization header and be rejected.
    final value = _key.text.trim();
    _key.text = value;
    await ref.read(voicePrefsProvider.notifier).setApiKey(value);
    if (!mounted) return;
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(
        content: Text(value.isEmpty ? 'API key removed' : 'API key saved'),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final prefs = ref.watch(voicePrefsProvider);
    // Prefs hydrate a frame after the screen opens, so initState can miss a
    // stored key. Seed the field once when it lands, and never overwrite what
    // the user is part-way through typing.
    ref.listen(voicePrefsProvider.select((p) => p.apiKey), (prev, next) {
      if ((prev ?? '').isEmpty && _key.text.isEmpty) _key.text = next;
    });
    final dirty = _key.text.trim() != prefs.apiKey;
    return Scaffold(
      appBar: AppBar(title: const Text('Voice Input')),
      body: CenteredBody(
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            _header('Provider'),
            RadioGroup<VoiceProvider>(
              groupValue: prefs.provider,
              onChanged: (v) {
                if (v != null) {
                  ref.read(voicePrefsProvider.notifier).setProvider(v);
                }
              },
              child: Column(
                children: [
                  for (final p in VoiceProvider.values)
                    RadioListTile<VoiceProvider>(
                      key: Key('voice-provider-${p.name}'),
                      contentPadding: EdgeInsets.zero,
                      value: p,
                      title: Text(p.label),
                    ),
                ],
              ),
            ),
            if (prefs.provider == VoiceProvider.openrouter) ...[
              const SizedBox(height: 24),
              _header('OpenRouter'),
              ..._openRouterBody(prefs, dirty),
            ],
            if (prefs.provider == VoiceProvider.system) ...[
              const SizedBox(height: 24),
              _header('System'),
              ..._systemBody(prefs),
            ],
            if (!prefs.enabled) ...[
              const SizedBox(height: 16),
              Text(
                prefs.provider == VoiceProvider.off
                    ? 'Pick a provider to turn on the mic button.'
                    : 'Add a key to turn on the mic button.',
                style: const TextStyle(color: AppColors.dim, fontSize: 12),
              ),
            ],
            if (prefs.enabled) ...[
              const SizedBox(height: 24),
              ..._tryItBody(prefs),
            ],
          ],
        ),
      ),
    );
  }

  Widget _header(String title) => Padding(
        padding: const EdgeInsets.only(bottom: 8),
        child: Text(
          title.toUpperCase(),
          style: const TextStyle(
            color: AppColors.accent,
            fontSize: 12,
            fontWeight: FontWeight.w700,
          ),
        ),
      );

  List<Widget> _openRouterBody(VoicePrefs prefs, bool dirty) => [
            TextField(
              key: const Key('openrouter-api-key'),
              controller: _key,
              obscureText: !_reveal,
              autocorrect: false,
              enableSuggestions: false,
              decoration: InputDecoration(
                labelText: 'OpenRouter API key',
                hintText: 'sk-or-v1-…',
                border: const OutlineInputBorder(),
                suffixIcon: IconButton(
                  icon: Icon(_reveal ? Icons.visibility_off : Icons.visibility),
                  tooltip: _reveal ? 'Hide' : 'Show',
                  onPressed: () => setState(() => _reveal = !_reveal),
                ),
              ),
              onSubmitted: (_) => dirty ? _save() : null,
            ),
            const SizedBox(height: 8),
            Row(
              children: [
                Expanded(
                  child: Text(
                    _status(dirty: dirty, saved: prefs.apiKey),
                    style: TextStyle(
                      color: dirty ? AppColors.text : AppColors.dim,
                      fontSize: 12,
                    ),
                  ),
                ),
                FilledButton(
                  key: const Key('save-api-key'),
                  onPressed: dirty ? _save : null,
                  child: const Text('Save'),
                ),
              ],
            ),
            const SizedBox(height: 8),
            const Text(
              'Your key, your account. Recordings go straight from this device '
              'to openrouter.ai — they do not pass through the gateway.',
              style: TextStyle(color: AppColors.dim, fontSize: 12),
            ),
            const SizedBox(height: 24),
            const Text(
              'Model — cheapest first',
              style: TextStyle(color: AppColors.dim, fontSize: 12),
            ),
            ref.watch(transcriptionModelsProvider).when(
                  data: (models) => _picker(models, prefs.model),
                  loading: () => const Padding(
                    padding: EdgeInsets.symmetric(vertical: 16),
                    child: LinearProgressIndicator(),
                  ),
                  error: (e, _) => _modelsError(e),
                ),
            const SizedBox(height: 8),
            const Text(
              'Prices are per minute of audio, billed to your OpenRouter '
              'account. Models marked /M tok charge per token instead, so their '
              'cost depends on how much you say.',
              style: TextStyle(color: AppColors.dim, fontSize: 12),
            ),
          ];

  List<Widget> _systemBody(VoicePrefs prefs) => [
        const Text(
          'Language',
          style: TextStyle(color: AppColors.dim, fontSize: 12),
        ),
        ref.watch(systemLanguagesProvider).when(
              data: (languages) => _languagePicker(languages, prefs.language),
              loading: () => const Padding(
                padding: EdgeInsets.symmetric(vertical: 16),
                child: LinearProgressIndicator(),
              ),
              error: (e, _) => _languagesError(e),
            ),
        switch (ref.watch(speechPackInstalledProvider(prefs.language))) {
          AsyncData(value: true) => const Padding(
              key: Key('speech-pack-installed'),
              padding: EdgeInsets.symmetric(vertical: 12),
              child: Row(
                children: [
                  Icon(Icons.check, size: 18, color: AppColors.dim),
                  SizedBox(width: 8),
                  Text(
                    'Speech pack installed',
                    style: TextStyle(color: AppColors.dim),
                  ),
                ],
              ),
            ),
          AsyncData(value: false) => Align(
              alignment: Alignment.centerLeft,
              child: TextButton.icon(
                key: const Key('download-speech-pack'),
                style: TextButton.styleFrom(padding: EdgeInsets.zero),
                icon: const Icon(Icons.download, size: 18),
                label: const Text('Download speech pack'),
                onPressed: () => _downloadPack(prefs.language),
              ),
            ),
          _ => const SizedBox.shrink(),
        },
        const SizedBox(height: 8),
        Text(
          Theme.of(context).platform == TargetPlatform.iOS
              ? 'Recognition runs on this device when it can. Otherwise '
                  'Apple processes the audio.'
              : 'Recognition runs on this device when it can. Otherwise '
                  'the system speech service processes the audio.',
          style: const TextStyle(color: AppColors.dim, fontSize: 12),
        ),
      ];

  Future<void> _downloadPack(String selected) async {
    final packs = ref.read(speechPacksProvider);
    final result = await packs.download(await packs.resolve(selected));
    if (!mounted) return;
    ref.invalidate(speechPackInstalledProvider(selected));
    ScaffoldMessenger.of(context).showSnackBar(
      SnackBar(content: Text(packDownloadMessage(result))),
    );
  }

  List<Widget> _tryItBody(VoicePrefs prefs) => [
        Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(child: _header('Try it')),
            TextButton(
              key: const Key('voice-try-clear'),
              style: TextButton.styleFrom(
                padding: EdgeInsets.zero,
                minimumSize: Size.zero,
                tapTargetSize: MaterialTapTargetSize.shrinkWrap,
              ),
              onPressed: _tryIt.clear,
              child: const Text('Clear'),
            ),
          ],
        ),
        VoiceInputField(
          controller: _tryIt,
          statusBelow: true,
          field: (suffixIcon) => TextField(
            key: const Key('voice-try-field'),
            controller: _tryIt,
            minLines: 3,
            maxLines: 6,
            decoration: InputDecoration(
              hintText: 'Hold the mic and speak',
              border: const OutlineInputBorder(),
              suffixIcon: suffixIcon,
            ),
          ),
        ),
        if (prefs.provider == VoiceProvider.openrouter) ...[
          const SizedBox(height: 8),
          const Text(
            'A test dictation is billed like a real one.',
            style: TextStyle(color: AppColors.dim, fontSize: 12),
          ),
        ],
      ];

  Widget _languagePicker(List<String> languages, String selected) {
    // Like the model picker: keep a stored language the recognizer no longer
    // lists, so the dropdown has a matching value.
    final items = selected.isEmpty || languages.contains(selected)
        ? languages
        : [...languages, selected];
    return DropdownButton<String>(
      key: const Key('system-language'),
      value: selected,
      isExpanded: true,
      menuMaxHeight: 336,
      items: [
        const DropdownMenuItem(value: '', child: Text('Device language')),
        for (final l in items) DropdownMenuItem(value: l, child: Text(l)),
      ],
      onChanged: (v) {
        if (v != null) ref.read(voicePrefsProvider.notifier).setLanguage(v);
      },
    );
  }

  Widget _languagesError(Object e) => Row(
        children: [
          Expanded(
            child: Text(
              'Could not load the language list: $e',
              style: const TextStyle(color: AppColors.dim, fontSize: 12),
            ),
          ),
          TextButton(
            onPressed: () => ref.invalidate(systemLanguagesProvider),
            child: const Text('Retry'),
          ),
        ],
      );

  /// What the line beside the Save button says. Names the destructive case
  /// outright, because an emptied field saves as "forget the key".
  String _status({required bool dirty, required String saved}) {
    if (!dirty) return saved.isEmpty ? 'No key saved' : 'Key saved';
    if (_key.text.trim().isEmpty) return 'Save will remove the stored key';
    return 'Unsaved changes';
  }

  Widget _picker(List<TranscriptionModel> models, String selected) {
    // A model can be retired while it is still the stored choice. Keep it in
    // the list so the dropdown has a matching value and the setting survives.
    final items = models.any((m) => m.id == selected)
        ? models
        : [...models, TranscriptionModel(selected, '$selected (unavailable)')];
    return DropdownButton<String>(
      key: const Key('transcription-model'),
      value: selected,
      isExpanded: true,
      // The catalog runs to ~22 models, which unconstrained fills the whole
      // screen. Cap it at about seven rows and let the rest scroll; the list is
      // cheapest-first, so the useful end is already at the top.
      menuMaxHeight: 336,
      items: [
        for (final m in items)
          DropdownMenuItem(
            value: m.id,
            child: Row(
              children: [
                Expanded(child: Text(m.name, overflow: TextOverflow.ellipsis)),
                if (m.priceLabel.isNotEmpty) ...[
                  const SizedBox(width: 8),
                  Text(
                    m.priceLabel,
                    style: const TextStyle(color: AppColors.dim, fontSize: 12),
                  ),
                ],
              ],
            ),
          ),
      ],
      onChanged: (v) {
        if (v != null) ref.read(voicePrefsProvider.notifier).setModel(v);
      },
    );
  }

  Widget _modelsError(Object e) => Row(
        children: [
          Expanded(
            child: Text(
              'Could not load the model list: $e',
              style: const TextStyle(color: AppColors.dim, fontSize: 12),
            ),
          ),
          TextButton(
            onPressed: () => ref.invalidate(transcriptionModelsProvider),
            child: const Text('Retry'),
          ),
        ],
      );
}
