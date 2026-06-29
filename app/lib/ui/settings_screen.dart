import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../pairing/gateway_store.dart';
import '../state/control.dart';
import '../state/gateway.dart';
import '../state/grouping.dart';
import '../state/push.dart';
import 'push_settings_screen.dart';
import 'responsive.dart';
import 'theme.dart';

const _monoStyle = TextStyle(fontFamily: 'monospace', color: AppColors.text);
const _dimStyle = TextStyle(color: AppColors.dim);

class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key, required this.store});
  final GatewayStore store;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final creds = ref.watch(credentialsProvider);
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: CenteredBody(
        child: Padding(
          padding: const EdgeInsets.all(16),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              _LabeledField(
                label: 'Gateway',
                child: SelectableText(
                  creds?.url ?? '(not paired)',
                  style: _monoStyle,
                ),
              ),
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
              const SizedBox(height: 24),
              ref.watch(serverInfoProvider).maybeWhen(
                    data: (info) => info == null ? _unavailable : _serverInfo(info),
                    loading: () => _versionRow,
                    orElse: () => _unavailable,
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
      ),
    );
  }
}

// Version-row placeholders shared by the loading and failed-fetch states.
const _versionRow = _LabeledField(
  label: 'Server Version',
  child: Text('…', style: _dimStyle),
);
const _unavailable = _LabeledField(
  label: 'Server Version',
  child: Text('(unavailable)', style: _dimStyle),
);

/// Renders the server version plus the connected-node list from server.info.
Widget _serverInfo(ServerInfo info) {
  return Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      _LabeledField(
        label: 'Server Version',
        child: SelectableText(
          info.version.isEmpty ? '(unknown)' : info.version,
          style: _monoStyle,
        ),
      ),
      const SizedBox(height: 24),
      _LabeledField(
        label: 'Nodes',
        child: info.nodes.isEmpty
            ? const Text('(none)', style: _dimStyle)
            : Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  for (final n in info.nodes)
                    Text(_nodeLine(n), style: _monoStyle),
                ],
              ),
      ),
    ],
  );
}

/// One node as "label · version · no tmux", omitting absent parts.
String _nodeLine(NodeRef n) => [
      n.label,
      if (n.version.isNotEmpty) n.version,
      if (!n.spawnSupported) 'no tmux',
    ].join(' · ');

/// A dim section label above a read-only value, the shared shape of the
/// settings screen's info rows (gateway URL, server version).
class _LabeledField extends StatelessWidget {
  const _LabeledField({required this.label, required this.child});
  final String label;
  final Widget child;

  @override
  Widget build(BuildContext context) => Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Text(label, style: const TextStyle(color: AppColors.dim, fontSize: 12)),
          const SizedBox(height: 4),
          child,
        ],
      );
}
