import 'package:flutter/material.dart';

import 'manual_entry_form.dart';
import 'pairing_uri.dart';
import 'scan_screen.dart';

class WelcomeScreen extends StatelessWidget {
  const WelcomeScreen({super.key, required this.onPaired});
  final void Function(GatewayCredentials) onPaired;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: Center(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            const Text('argus', style: TextStyle(fontSize: 28, fontWeight: FontWeight.bold)),
            const SizedBox(height: 8),
            const Text('supervise Claude Code from your phone'),
            const SizedBox(height: 24),
            FilledButton(
              onPressed: () async {
                final c = await Navigator.of(context).push<GatewayCredentials>(
                  MaterialPageRoute(builder: (_) => const ScanScreen()),
                );
                if (c != null) onPaired(c);
              },
              child: const Text('Scan QR code'),
            ),
            TextButton(
              onPressed: () => showModalBottomSheet<void>(
                context: context,
                isScrollControlled: true,
                builder: (_) => ManualEntryForm(onSubmit: (c) {
                  Navigator.of(context).pop();
                  onPaired(c);
                }),
              ),
              child: const Text('Enter URL + token'),
            ),
          ],
        ),
      ),
    );
  }
}
