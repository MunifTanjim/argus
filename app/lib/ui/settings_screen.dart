import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../pairing/gateway_store.dart';
import '../state/gateway.dart';
import '../state/push.dart';
import 'push_settings_screen.dart';
import 'theme.dart';

class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key, required this.store});
  final GatewayStore store;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final creds = ref.watch(credentialsProvider);
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('Gateway',
                style: TextStyle(color: AppColors.dim, fontSize: 12)),
            const SizedBox(height: 4),
            SelectableText(creds?.url ?? '(not paired)',
                style: const TextStyle(
                    fontFamily: 'monospace', color: AppColors.text)),
            const SizedBox(height: 24),
            ListTile(
              contentPadding: EdgeInsets.zero,
              leading: const Icon(Icons.notifications_outlined),
              title: const Text('Push notifications'),
              subtitle: const Text('UnifiedPush distributor or FCM'),
              trailing: const Icon(Icons.chevron_right),
              onTap: () => Navigator.of(context).push(
                MaterialPageRoute(builder: (_) => const PushSettingsScreen()),
              ),
            ),
            const Spacer(),
            OutlinedButton(
              onPressed: () async {
                // Drop the device's push registration while still connected.
                await ref.read(pushControllerProvider).unregister();
                await store.clear();
                ref.read(credentialsProvider.notifier).state = null;
              },
              child: const Text('Unpair'),
            ),
          ],
        ),
      ),
    );
  }
}
