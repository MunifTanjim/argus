import 'package:flutter/material.dart';

import '../state/gateway.dart' show NodeSshPrompter;

/// Asks about node SSH host keys in dialogs over the app's navigator.
class DialogNodeSshPrompter implements NodeSshPrompter {
  DialogNodeSshPrompter(this._navigatorKey);

  final GlobalKey<NavigatorState> _navigatorKey;

  @override
  Future<bool?> confirmHostKey(String hostPort, String fingerprint) async {
    final context = _navigatorKey.currentContext;
    if (context == null) throw StateError('no navigator to ask with');
    // Dismissed without a choice: null.
    return showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Trust SSH host key?'),
        content: SelectableText(
          'First connection to $hostPort for a node link.\n\n$fingerprint',
        ),
        actions: [
          TextButton(
            key: const Key('node-ssh-decline'),
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Decline'),
          ),
          TextButton(
            key: const Key('node-ssh-trust'),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('Trust'),
          ),
        ],
      ),
    );
  }

  @override
  Future<bool> hostKeyChanged(String hostPort, String message) async {
    final context = _navigatorKey.currentContext;
    if (context == null) return false;
    final forget = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('SSH host key changed'),
        content: Text(
          '$message\n\nThe node stays on the gateway relay. Forget the key only '
          'if you know why it changed.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.of(ctx).pop(false),
            child: const Text('Close'),
          ),
          TextButton(
            key: const Key('node-ssh-forget'),
            style: TextButton.styleFrom(
              foregroundColor: Theme.of(ctx).colorScheme.error,
            ),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('Forget key'),
          ),
        ],
      ),
    );
    return forget ?? false;
  }
}
