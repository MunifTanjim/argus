import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/appearance.dart';
import 'responsive.dart';

class AppearanceScreen extends ConsumerWidget {
  const AppearanceScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final verbose = ref.watch(
      appearancePrefsProvider.select((p) => p.verboseTranscript),
    );
    return Scaffold(
      appBar: AppBar(title: const Text('Appearance')),
      body: CenteredBody(
        child: ListView(
          padding: const EdgeInsets.symmetric(vertical: 8),
          children: [
            SwitchListTile(
              value: verbose,
              onChanged: ref
                  .read(appearancePrefsProvider.notifier)
                  .setVerboseTranscript,
              title: const Text('Verbose transcript'),
              subtitle: const Text('Expand thinking and tool runs by default'),
            ),
          ],
        ),
      ),
    );
  }
}
