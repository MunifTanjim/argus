import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/control.dart';
import '../state/gateway.dart';
import '../state/profiles.dart';
import '../state/grouping.dart';
import '../transport/ssh_gateway.dart';
import 'appearance_screen.dart';
import 'device_identity_screen.dart';
import 'push_settings_screen.dart';
import 'responsive.dart';
import 'theme.dart';

const _monoStyle = TextStyle(fontFamily: 'monospace', color: AppColors.text);
const _dimStyle = TextStyle(color: AppColors.dim);

class SettingsScreen extends ConsumerWidget {
  const SettingsScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final creds = ref.watch(credentialsProvider);
    // Parse the SSH host:port once here; reused as both the visibility guard
    // and the value the forget action operates on. isSshGatewayUrl only checks
    // the scheme, so guard the full parse — a malformed persisted ssh:// url
    // must not crash the settings screen render.
    final sshHostPortValue = _sshHostPortOrNull(creds?.url);
    return Scaffold(
      appBar: AppBar(title: const Text('Settings')),
      body: CenteredBody(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Expanded(
              child: SingleChildScrollView(
                padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
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
                      leading: const Icon(Icons.palette_outlined),
                      title: const Text('Appearance'),
                      subtitle: const Text('Transcript display options'),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: () => Navigator.of(context).push(
                        MaterialPageRoute(
                            builder: (_) => const AppearanceScreen()),
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
                        MaterialPageRoute(
                            builder: (_) => const PushSettingsScreen()),
                      ),
                    ),
                    const SizedBox(height: 24),
                    ListTile(
                      contentPadding: EdgeInsets.zero,
                      leading: const Icon(Icons.key_outlined),
                      title: const Text('Device identity'),
                      subtitle: const Text('Enroll & verify this device'),
                      trailing: const Icon(Icons.chevron_right),
                      onTap: () => Navigator.of(context).push(
                        MaterialPageRoute(
                            builder: (_) => const DeviceIdentityScreen()),
                      ),
                    ),
                    const SizedBox(height: 24),
                    ref.watch(serverInfoProvider).maybeWhen(
                          data: (info) =>
                              info == null ? _unavailable : _serverInfo(info),
                          loading: () => _versionRow,
                          orElse: () => _unavailable,
                        ),
                  ],
                ),
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  if (sshHostPortValue != null) ...[
                    OutlinedButton(
                      key: const Key('forget-host-key'),
                      onPressed: () async {
                        await ref
                            .read(hostKeyStoreProvider)
                            .forget(sshHostPortValue);
                        // Redial now so a connection stuck on ConnState.failed
                        // (rejected host key) recovers without waiting for a
                        // resume; the new key is re-pinned trust-on-first-use.
                        ref.read(gatewayProvider)?.reconnectNow();
                        if (!context.mounted) return;
                        ScaffoldMessenger.of(context).showSnackBar(SnackBar(
                          content: Text(
                            'Forgot host key for $sshHostPortValue. Reconnecting; it will be re-pinned.',
                          ),
                        ));
                      },
                      child: const Text('Forget SSH host key'),
                    ),
                    const SizedBox(height: 8),
                  ],
                  OutlinedButton(
                    onPressed: () async {
                      // Clearing credentials disposes the gateway, which
                      // unregisters this device from it (see gatewayProvider).
                      await ref.read(profileStoreProvider).clearActiveId();
                      ref.read(credentialsProvider.notifier).state = null;
                    },
                    child: const Text('Disconnect'),
                  ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}

/// The `host:port` of an ssh gateway url, or null if [url] is absent, not an
/// ssh url, or malformed. Never throws — safe to call from build().
String? _sshHostPortOrNull(String? url) {
  if (url == null || !isSshGatewayUrl(url)) return null;
  try {
    return sshHostPort(parseSshGatewayUrl(url));
  } on FormatException {
    return null;
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
