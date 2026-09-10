import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../e2e/e2e.dart';
import '../state/device_identity.dart';
import '../state/gateway.dart';
import 'responsive.dart';
import 'theme.dart';

/// Spells a 32-byte key as keyfmt does on the CLI: prefix + lowercase hex (see
/// internal/keyfmt). This is what `argus lock status` prints and `argus lock
/// sign` parses, so the value pastes verbatim.
String _keyfmt(String prefix, List<int> bytes) =>
    '$prefix${bytes.map((b) => b.toRadixString(16).padLeft(2, '0')).join()}';

class DeviceIdentityScreen extends ConsumerWidget {
  const DeviceIdentityScreen({super.key});

  static void _copy(BuildContext context, String text) {
    unawaited(Clipboard.setData(ClipboardData(text: text)));
    ScaffoldMessenger.of(context).showSnackBar(
        const SnackBar(content: Text('Copied to clipboard')));
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final summary = ref.watch(trustSummaryProvider);
    final identityAsync = ref.watch(deviceIdentityProvider);

    final status = trustStatusOf(summary);
    final isAwaiting = status == TrustStatus.awaitingAuthorization;
    final enrollExpanded = isAwaiting || !summary.connected;

    return Scaffold(
      appBar: AppBar(title: const Text('Device trust')),
      body: CenteredBody(
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            _StatusCard(summary: summary),
            if (status == TrustStatus.supersededLiveRoot ||
                status == TrustStatus.supersededNoRoot) ...[
              const SizedBox(height: 8),
              _SupersededCard(summary: summary, canAdopt: status == TrustStatus.supersededLiveRoot),
            ],
            if (summary.equivocation) ...[
              const SizedBox(height: 8),
              const _EquivocationBanner(),
            ],
            const SizedBox(height: 8),
            ExpansionTile(
              initiallyExpanded: enrollExpanded,
              title: const Text('Enroll this device'),
              children: [
                identityAsync.when(
                  data: (kp) => _EnrollBody(kp: kp),
                  loading: () => const Padding(
                    padding: EdgeInsets.all(16),
                    child: CircularProgressIndicator(),
                  ),
                  error: (e, _) => Padding(
                    padding: const EdgeInsets.all(16),
                    child: Text('Error: $e',
                        style: const TextStyle(color: AppColors.error)),
                  ),
                ),
              ],
            ),
            if (summary.signers.isNotEmpty && trustSignersVerifiable(status))
              ExpansionTile(
                title: const Text('Verify trust'),
                children: [_VerifyBody(summary: summary)],
              ),
            const ExpansionTile(
              title: Text('Advanced'),
              children: [_AdvancedBody()],
            ),
          ],
        ),
      ),
    );
  }
}

class _StatusCard extends StatelessWidget {
  const _StatusCard({required this.summary});

  final TrustSummary summary;

  @override
  Widget build(BuildContext context) {
    final (label, icon, color) = _statusDisplay(summary);
    return Card(
      color: AppColors.card,
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        child: Row(
          children: [
            Icon(icon, color: color, size: 20),
            const SizedBox(width: 12),
            Text(label,
                style: TextStyle(
                    color: color, fontWeight: FontWeight.bold, fontSize: 15)),
          ],
        ),
      ),
    );
  }

  static (String, IconData, Color) _statusDisplay(TrustSummary summary) {
    const green = Color(0xFFb8bb26); // gruvbox green
    const yellow = Color(0xFFfabd2f); // gruvbox yellow
    switch (trustStatusOf(summary)) {
      case TrustStatus.notConnected:
        return ('Not connected', Icons.cloud_off_outlined, AppColors.dim);
      case TrustStatus.openNetwork:
        return ('Open network', Icons.lock_open_outlined, AppColors.secondary);
      case TrustStatus.supersededLiveRoot:
      case TrustStatus.supersededNoRoot:
        return ('Trust root superseded', Icons.sync_problem_outlined, yellow);
      case TrustStatus.disabled:
        return ('Disabled', Icons.block_outlined, AppColors.dim);
      case TrustStatus.authorized:
        return ('Authorized', Icons.verified_outlined, green);
      case TrustStatus.awaitingAuthorization:
        return ('Awaiting authorization', Icons.pending_outlined, yellow);
    }
  }
}

/// Mirrors the CLI: name the successor root, require an explicit re-pin, never
/// adopt silently. [canAdopt] is false when no live successor exists yet.
class _SupersededCard extends ConsumerWidget {
  const _SupersededCard({required this.summary, required this.canAdopt});

  final TrustSummary summary;
  final bool canAdopt;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final genesis = summary.supersededByGenesis;
    final words = genesis == null ? '' : genesisFingerprintWords(genesis).join(' ');
    return Card(
      color: AppColors.card,
      child: Padding(
        padding: const EdgeInsets.all(16),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(
              canAdopt
                  ? 'This device is pinned to a trust root that was disabled. The '
                      'network re-locked under a new root. Compare the fingerprint '
                      'below against a trusted device, then re-establish trust.'
                  : 'This device is pinned to a trust root that was disabled. The '
                      'network has not re-locked yet. Wait for a signer to re-lock, '
                      'then re-establish trust.',
              style: const TextStyle(color: AppColors.text),
            ),
            if (genesis != null) ...[
              const SizedBox(height: 12),
              const Text('New root fingerprint',
                  style: TextStyle(color: AppColors.dim, fontSize: 12)),
              const SizedBox(height: 4),
              SelectableText(words,
                  style: const TextStyle(fontFamily: 'monospace', fontSize: 13)),
            ],
            if (canAdopt) ...[
              const SizedBox(height: 12),
              FilledButton(
                onPressed: () => _confirmReestablish(context, ref, words),
                child: const Text('Re-establish trust'),
              ),
            ],
          ],
        ),
      ),
    );
  }

  Future<void> _confirmReestablish(BuildContext ctx, WidgetRef r, String words) async {
    final ok = await showDialog<bool>(
      context: ctx,
      builder: (dialogCtx) => AlertDialog(
        title: const Text('Re-establish trust?'),
        content: Text(
          'This device will adopt the new trust root:\n\n$words\n\n'
          'Only continue if this fingerprint matches a device you trust.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogCtx).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(dialogCtx).pop(true),
            child: const Text('Re-establish'),
          ),
        ],
      ),
    );
    if (ok != true || !ctx.mounted) return;
    final client = r.read(gatewayProvider)?.client;
    final adopted = client is E2EClient && await client.adoptSupersedingRoot();
    if (adopted) {
      // Reconnect so the client rebuilds from the newly-pinned root and opens
      // channels to the now-authorized nodes. adopt alone re-pins but never opens
      // channels (_reevaluateChannels only closes), so sessions would stay empty.
      // Mirrors the CLI: `argus lock pin` then restart.
      r.read(gatewayProvider)?.reconnectNow();
    }
    if (!ctx.mounted) return;
    ScaffoldMessenger.of(ctx).showSnackBar(SnackBar(
      content: Text(adopted
          ? 'Trust re-established; reconnecting.'
          : 'Could not re-establish trust; try again after the next sync.'),
    ));
  }
}

class _EquivocationBanner extends StatelessWidget {
  const _EquivocationBanner();

  @override
  Widget build(BuildContext context) {
    return Card(
      color: AppColors.awaitingSurface,
      shape: RoundedRectangleBorder(
        borderRadius: BorderRadius.circular(12),
        side: const BorderSide(color: AppColors.awaitingBorder),
      ),
      child: const Padding(
        padding: EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Icon(Icons.warning_amber_outlined,
                    color: Color(0xFFfe8019), size: 18),
                SizedBox(width: 8),
                Expanded(
                  child: Text(
                    'Trust-log equivocation detected',
                    style: TextStyle(
                      color: Color(0xFFfe8019),
                      fontWeight: FontWeight.bold,
                      fontSize: 14,
                    ),
                  ),
                ),
              ],
            ),
            SizedBox(height: 8),
            Text(
              'The gateway may be showing inconsistent trust-log views across '
              'your nodes — the app cannot fully verify what it relays. '
              'Compare the fingerprint words on each node by running '
              '`argus lock status` and confirming they match. '
              'The app continues to work normally.',
              style: TextStyle(color: AppColors.secondary, fontSize: 13),
            ),
          ],
        ),
      ),
    );
  }
}

class _EnrollBody extends StatelessWidget {
  const _EnrollBody({required this.kp});

  final KeyPair kp;

  @override
  Widget build(BuildContext context) {
    final pubKey = _keyfmt('devpub:', kp.publicKey);
    final command = 'argus lock sign $pubKey';
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          const Text('Device public key',
              style: TextStyle(color: AppColors.dim, fontSize: 12)),
          const SizedBox(height: 4),
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(child: SelectableText(pubKey, style: mono)),
              IconButton(
                icon: const Icon(Icons.copy_outlined),
                tooltip: 'Copy key',
                onPressed: () => DeviceIdentityScreen._copy(context, pubKey),
              ),
            ],
          ),
          const SizedBox(height: 12),
          const Text('Authorization command',
              style: TextStyle(color: AppColors.dim, fontSize: 12)),
          const SizedBox(height: 4),
          Row(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Expanded(child: SelectableText(command, style: mono)),
              IconButton(
                icon: const Icon(Icons.copy_outlined),
                tooltip: 'Copy command',
                onPressed: () => DeviceIdentityScreen._copy(context, command),
              ),
            ],
          ),
          const SizedBox(height: 8),
          const Text(
            'Run this command on a signer node to authorize this device.',
            style: TextStyle(color: AppColors.dim, fontSize: 12),
          ),
        ],
      ),
    );
  }
}

class _VerifyBody extends StatelessWidget {
  const _VerifyBody({required this.summary});

  final TrustSummary summary;

  @override
  Widget build(BuildContext context) {
    final words = signerSetFingerprintWords(summary.signers);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Wrap(
            spacing: 8,
            runSpacing: 8,
            children: [
              for (final w in words)
                Chip(
                  label: Text(w,
                      style: const TextStyle(fontWeight: FontWeight.bold)),
                ),
            ],
          ),
          const SizedBox(height: 8),
          const Text('trusted signers',
              style: TextStyle(color: AppColors.dim, fontSize: 12)),
          const SizedBox(height: 4),
          for (final signer in summary.signers)
            SelectableText(_keyfmt('sigpub:', signer),
                style: mono.copyWith(fontSize: 10, color: AppColors.dim)),
          const SizedBox(height: 8),
          const Text(
            'Compare these words with `argus lock status` on a signer node.',
            style: TextStyle(color: AppColors.dim, fontSize: 12),
          ),
        ],
      ),
    );
  }
}

class _AdvancedBody extends ConsumerWidget {
  const _AdvancedBody();

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 0, 16, 16),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          OutlinedButton(
            onPressed: () => _confirmReset(context, ref),
            child: const Text('Reset trust anchor'),
          ),
        ],
      ),
    );
  }

  Future<void> _confirmReset(BuildContext ctx, WidgetRef r) async {
    final ok = await showDialog<bool>(
      context: ctx,
      builder: (dialogCtx) => AlertDialog(
        title: const Text('Reset trust anchor?'),
        content: const Text(
          'This clears the stored trust chain. The device will re-establish '
          'trust on the next connection.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(dialogCtx).pop(false),
            child: const Text('Cancel'),
          ),
          TextButton(
            onPressed: () => Navigator.of(dialogCtx).pop(true),
            child: const Text('Reset'),
          ),
        ],
      ),
    );
    if (ok != true) return;
    if (!ctx.mounted) return;
    await r.read(trustChainStoreProvider).clear();
    if (!ctx.mounted) return;
    r.read(gatewayProvider)?.reconnectNow();
    ScaffoldMessenger.of(ctx).showSnackBar(const SnackBar(
      content: Text(
          'Trust anchor cleared; reconnecting — it will be re-established on first use.'),
    ));
  }
}
