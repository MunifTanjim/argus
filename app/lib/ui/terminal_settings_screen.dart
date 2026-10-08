import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/terminal_prefs.dart';

class TerminalSettingsScreen extends ConsumerWidget {
  const TerminalSettingsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final prefs = ref.watch(terminalPrefsProvider);
    return Scaffold(
      appBar: AppBar(title: const Text('Terminal')),
      body: SafeArea(
        top: false,
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            Text('Emulator', style: Theme.of(context).textTheme.titleSmall),
            RadioGroup<TerminalEmulator>(
              groupValue: prefs.emulator,
              onChanged: (v) {
                if (v != null) {
                  ref.read(terminalPrefsProvider.notifier).setEmulator(v);
                }
              },
              child: Column(
                children: [
                  for (final e in TerminalEmulator.values)
                    RadioListTile<TerminalEmulator>(
                      key: Key('terminal-emulator-${e.name}'),
                      contentPadding: EdgeInsets.zero,
                      value: e,
                      title: Text(e.label),
                    ),
                ],
              ),
            ),
            const SizedBox(height: 24),
            Row(
              children: [
                Expanded(
                  child: Text('Font size',
                      style: Theme.of(context).textTheme.titleSmall),
                ),
                Text(prefs.fontSize.round().toString()),
              ],
            ),
            Slider(
              min: terminalFontSizeMin,
              max: terminalFontSizeMax,
              divisions: (terminalFontSizeMax - terminalFontSizeMin).round(),
              value: prefs.fontSize,
              onChanged: (v) => ref
                  .read(terminalPrefsProvider.notifier)
                  .setFontSize(v.roundToDouble()),
            ),
          ],
        ),
      ),
    );
  }
}
