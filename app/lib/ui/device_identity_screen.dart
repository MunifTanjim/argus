import 'dart:async';
import 'dart:convert';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../e2e/e2e.dart';
import '../state/device_identity.dart';
import '../state/gateway.dart';
import 'responsive.dart';
import 'theme.dart';

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

    final isAwaiting = summary.connected &&
        summary.isLocked == true &&
        !summary.isAuthorized &&
        !summary.isDisabled;
    final enrollExpanded = isAwaiting || !summary.connected;

    return Scaffold(
      appBar: AppBar(title: const Text('Device identity')),
      body: CenteredBody(
        child: ListView(
          padding: const EdgeInsets.all(16),
          children: [
            _StatusCard(summary: summary),
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
            ExpansionTile(
              enabled: summary.signers.isNotEmpty,
              title: const Text('Verify trust'),
              children: summary.signers.isNotEmpty
                  ? [_VerifyBody(summary: summary)]
                  : const [],
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
    if (!summary.connected) {
      return ('Not connected', Icons.cloud_off_outlined, AppColors.dim);
    }
    if (summary.isLocked == null) {
      return ('Open network', Icons.lock_open_outlined, AppColors.secondary);
    }
    if (summary.isDisabled) {
      return ('Disabled', Icons.block_outlined, AppColors.dim);
    }
    if (summary.isAuthorized) {
      return (
        'Authorized',
        Icons.verified_outlined,
        const Color(0xFFb8bb26), // gruvbox green
      );
    }
    return (
      'Awaiting authorization',
      Icons.pending_outlined,
      const Color(0xFFfabd2f), // gruvbox yellow
    );
  }
}

class _EnrollBody extends StatelessWidget {
  const _EnrollBody({required this.kp});

  final KeyPair kp;

  @override
  Widget build(BuildContext context) {
    final pubB64 = base64.encode(kp.publicKey);
    final command = 'argus lock sign $pubB64';
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
              Expanded(child: SelectableText(pubB64, style: mono)),
              IconButton(
                icon: const Icon(Icons.copy_outlined),
                tooltip: 'Copy key',
                onPressed: () => DeviceIdentityScreen._copy(context, pubB64),
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
            SelectableText(base64.encode(signer),
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
