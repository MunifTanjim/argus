import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/appearance.dart';
import 'responsive.dart';

class AppearanceScreen extends ConsumerWidget {
  const AppearanceScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final collapse = ref.watch(
        appearancePrefsProvider.select((p) => p.collapseToolCalls));
    return Scaffold(
      appBar: AppBar(title: const Text('Appearance')),
      body: CenteredBody(
        child: ListView(
          padding: const EdgeInsets.symmetric(vertical: 8),
          children: [
            SwitchListTile(
              value: collapse,
              onChanged: ref
                  .read(appearancePrefsProvider.notifier)
                  .setCollapseToolCalls,
              title: const Text('Collapse tool calls'),
              subtitle: const Text(
                  'Hide tool calls in assistant messages behind a tap'),
            ),
          ],
        ),
      ),
    );
  }
}
