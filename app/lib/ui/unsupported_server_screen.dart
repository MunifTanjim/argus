import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/compat.dart';
import '../state/control.dart';
import 'responsive.dart';
import 'sign_out.dart';

/// Shows [child], or [UnsupportedServerScreen] while the server is older than
/// [minServerVersion]. An unknown server (not yet fetched, fetch failed) passes.
class ServerGate extends ConsumerWidget {
  const ServerGate({super.key, required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    // The version of an unsupported server, else null; rebuilds only on a flip.
    final unsupported = ref.watch(serverInfoProvider.select(
      (a) => switch (a.value) {
        final info? when !info.supported => info.version,
        _ => null,
      },
    ));
    return unsupported == null ? child : UnsupportedServerScreen(version: unsupported);
  }
}

/// Shown in place of the app when the server is older than [minServerVersion].
class UnsupportedServerScreen extends ConsumerWidget {
  const UnsupportedServerScreen({super.key, required this.version});

  final String version;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final theme = Theme.of(context);
    return Scaffold(
      body: SafeArea(
        child: CenteredBody(
          maxWidth: 420,
          child: Padding(
            padding: const EdgeInsets.all(24),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Icon(Icons.system_update, size: 48, color: theme.colorScheme.error),
                const SizedBox(height: 16),
                Text(
                  'Server too old',
                  style: theme.textTheme.titleLarge,
                  textAlign: TextAlign.center,
                ),
                const SizedBox(height: 8),
                Text(
                  'This app needs argus $minServerVersion or newer; the server '
                  'runs $version. Upgrade argus on the server, then retry.',
                  textAlign: TextAlign.center,
                ),
                const SizedBox(height: 24),
                FilledButton(
                  onPressed: () => ref.invalidate(serverInfoProvider),
                  child: const Text('Retry'),
                ),
                const SizedBox(height: 8),
                OutlinedButton(
                  onPressed: () => disconnect(ref),
                  child: const Text('Disconnect'),
                ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}
