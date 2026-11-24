import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/compat.dart';
import '../state/control.dart';
import '../state/gateway.dart';
import '../state/grouping.dart';
import '../transport/ssh_gateway.dart';
import 'about_screen.dart';
import 'appearance_screen.dart';
import 'device_identity_screen.dart';
import 'push_settings_screen.dart';
import 'terminal_settings_screen.dart';
import 'responsive.dart';
import 'sign_out.dart';
import 'theme.dart';
import 'upgrade_tile.dart';
import 'voice_screen.dart';

TextStyle _monoStyle(BuildContext context) =>
    TextStyle(fontFamily: monoFamily, color: context.tokens.text);
TextStyle _dimStyle(BuildContext context) =>
    TextStyle(color: context.tokens.textDim);

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
      body: SafeArea(
        top: false,
        child: CenteredBody(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(
                child: SingleChildScrollView(
                  padding: const EdgeInsets.fromLTRB(16, 16, 16, 8),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: [
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
                        leading: const Icon(Icons.terminal),
                        title: const Text('Terminal'),
                        subtitle: const Text('Emulator for live screens'),
                        trailing: const Icon(Icons.chevron_right),
                        onTap: () => Navigator.of(context).push(
                          MaterialPageRoute(
                              builder: (_) => const TerminalSettingsScreen()),
                        ),
                      ),
                      const SizedBox(height: 24),
                      ListTile(
                        contentPadding: EdgeInsets.zero,
                        leading: const Icon(Icons.notifications_outlined),
                        title: const Text('Push Notifications'),
                        subtitle: const Text('Delivery method and preferences'),
                        trailing: const Icon(Icons.chevron_right),
                        onTap: () => Navigator.of(context).push(
                          MaterialPageRoute(
                              builder: (_) => const PushSettingsScreen()),
                        ),
                      ),
                      const SizedBox(height: 24),
                      ListTile(
                        contentPadding: EdgeInsets.zero,
                        leading: const Icon(Icons.mic_none),
                        title: const Text('Voice Input'),
                        subtitle: const Text('Transcription provider and model'),
                        trailing: const Icon(Icons.chevron_right),
                        onTap: () => Navigator.of(context).push(
                          MaterialPageRoute(builder: (_) => const VoiceScreen()),
                        ),
                      ),
                      const SizedBox(height: 24),
                      ListTile(
                        contentPadding: EdgeInsets.zero,
                        leading: const Icon(Icons.key_outlined),
                        title: const Text('Device Trust'),
                        subtitle: const Text('Status, enrollment & recovery'),
                        trailing: const Icon(Icons.chevron_right),
                        onTap: () => Navigator.of(context).push(
                          MaterialPageRoute(
                              builder: (_) => const DeviceIdentityScreen()),
                        ),
                      ),
                      const SizedBox(height: 24),
                      ListTile(
                        contentPadding: EdgeInsets.zero,
                        leading: const Icon(Icons.info_outline),
                        title: const Text('About'),
                        subtitle: const Text('Privacy policy & licenses'),
                        trailing: const Icon(Icons.chevron_right),
                        onTap: () => Navigator.of(context).push(
                          MaterialPageRoute(builder: (_) => const AboutScreen()),
                        ),
                      ),
                      const SizedBox(height: 24),
                      _LabeledField(
                        label: 'Gateway',
                        child: SelectableText(
                          creds?.url ?? '(not paired)',
                          style: _monoStyle(context),
                        ),
                      ),
                      const SizedBox(height: 24),
                      ref.watch(serverInfoProvider).maybeWhen(
                            data: (info) =>
                                info == null ? _unavailable(context) : _serverInfo(context, info),
                            loading: () => _versionRow(context),
                            orElse: () => _unavailable(context),
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
                      onPressed: () => disconnect(ref),
                      child: const Text('Disconnect'),
                    ),
                  ],
                ),
              ),
            ],
          ),
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
Widget _versionRow(BuildContext context) => _LabeledField(
  label: 'Server Version',
  child: Text('…', style: _dimStyle(context)),
);
Widget _unavailable(BuildContext context) => _LabeledField(
  label: 'Server Version',
  child: Text('(unavailable)', style: _dimStyle(context)),
);

/// Renders the server version plus the connected-node list from server.info.
Widget _serverInfo(BuildContext context, ServerInfo info) {
  return Column(
    crossAxisAlignment: CrossAxisAlignment.start,
    children: [
      _LabeledField(
        label: 'Server Version',
        child: SelectableText(
          info.version.isEmpty ? '(unknown)' : info.version,
          style: _monoStyle(context),
        ),
      ),
      UpgradeTile(
        key: const ValueKey('upgrade-gateway'),
        nodeId: null,
        label: 'the gateway',
        version: info.version,
      ),
      const SizedBox(height: 24),
      _LabeledField(
        label: 'Nodes',
        child: info.nodes.isEmpty
            ? Text('(none)', style: _dimStyle(context))
            : Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  for (final n in info.nodes)
                    Text(_nodeLine(n), style: _monoStyle(context)),
                ],
              ),
      ),
    ],
  );
}

/// One node as "label · version · no tmux" (or "outdated"), omitting absent parts.
String _nodeLine(NodeRef n) => [
      n.label,
      if (n.version.isNotEmpty) n.version,
      if (n.outdated) 'outdated, needs $minNodeVersion+'
      else if (!n.spawnSupported) 'no tmux',
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
          Text(label, style: TextStyle(color: context.tokens.textDim, fontSize: 12)),
          const SizedBox(height: 4),
          child,
        ],
      );
}
