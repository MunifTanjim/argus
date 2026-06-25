import 'package:flutter/material.dart';

import '../ui/responsive.dart';
import 'pairing_uri.dart';

class ManualEntryForm extends StatefulWidget {
  const ManualEntryForm({super.key, required this.onSubmit});
  final void Function(GatewayCredentials) onSubmit;

  @override
  State<ManualEntryForm> createState() => _ManualEntryFormState();
}

class _ManualEntryFormState extends State<ManualEntryForm> {
  final _url = TextEditingController();
  final _token = TextEditingController();

  @override
  void dispose() {
    _url.dispose();
    _token.dispose();
    super.dispose();
  }

  void _submit() {
    final url = _url.text.trim();
    final token = _token.text.trim();
    if (url.isEmpty || token.isEmpty) return;
    widget.onSubmit(GatewayCredentials(url, token));
  }

  @override
  Widget build(BuildContext context) {
    return CenteredBody(
      maxWidth: 480,
      child: Padding(
        padding: EdgeInsets.fromLTRB(
          16,
          16,
          16,
          16 +
              MediaQuery.of(context).viewInsets.bottom +
              MediaQuery.of(context).padding.bottom,
        ),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            TextField(
              key: const Key('url'),
              controller: _url,
              decoration: const InputDecoration(labelText: 'Gateway URL'),
            ),
            TextField(
              key: const Key('token'),
              controller: _token,
              obscureText: true,
              decoration: const InputDecoration(labelText: 'Token'),
            ),
            const SizedBox(height: 16),
            FilledButton(
              key: const Key('connect'),
              onPressed: _submit,
              child: const Text('Connect'),
            ),
          ],
        ),
      ),
    );
  }
}
