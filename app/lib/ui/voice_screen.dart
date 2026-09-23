import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/openrouter.dart';
import '../state/voice.dart';
import 'responsive.dart';
import 'theme.dart';

/// Settings for dictation: the user's own OpenRouter key and the speech-to-text
/// model it pays for.
class VoiceScreen extends ConsumerStatefulWidget {
  const VoiceScreen({super.key});

  @override
  ConsumerState<VoiceScreen> createState() => _VoiceScreenState();
}

class _VoiceScreenState extends ConsumerState<VoiceScreen> {
  late final TextEditingController _key;
  bool _reveal = false;

  @override
  void initState() {
    super.initState();
    _key = TextEditingController(text: ref.read(voicePrefsProvider).apiKey)
      ..addListener(_onEdit);
  }

  // Rebuild so the Save button and the status line follow what is typed.
  void _onEdit() => setState(() {});

  @override
  void dispose() {
    _key.removeListener(_onEdit);
    _key.dispose();
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
            const SizedBox(height: 16),
            Text(
              prefs.enabled
                  ? 'The mic button appears next to the reply and new-session '
                      'prompt fields.'
                  : 'Add a key to turn on the mic button.',
              style: const TextStyle(color: AppColors.dim, fontSize: 12),
            ),
          ],
        ),
      ),
    );
  }

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
