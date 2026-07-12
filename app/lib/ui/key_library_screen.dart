import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../pairing/profile.dart';
import '../state/profiles.dart';
import '../transport/library_key.dart';
import '../transport/ssh_key_store.dart';
import '../transport/ssh_keygen.dart';
import '../transport/ssh_tunnel.dart';
import '../util/id.dart';

class KeyLibraryScreen extends ConsumerWidget {
  const KeyLibraryScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final keys = ref.watch(keysProvider);
    return Scaffold(
      appBar: AppBar(title: const Text('SSH keys')),
      body: keys.when(
        loading: () => const Center(child: CircularProgressIndicator()),
        error: (e, _) => Center(child: Text('$e')),
        data: (list) => ListView(
          children: [
            for (final k in list)
              ListTile(
                key: Key('key-${k.id}'),
                title: Text(k.name),
                subtitle: k.passphrase != null ? const Text('passphrase set') : null,
                onTap: () => showDialog(
                  context: context,
                  builder: (_) => _PublicKeyDialog(libKey: k),
                ),
                trailing: IconButton(
                  key: Key('key-delete-${k.id}'),
                  icon: const Icon(Icons.delete_outline),
                  onPressed: () => _confirmDelete(context, ref, k),
                ),
              ),
            const Divider(),
            _GenerateTile(),
            const _ImportTile(),
          ],
        ),
      ),
    );
  }

  Future<void> _confirmDelete(
      BuildContext context, WidgetRef ref, LibraryKey k) async {
    final profiles = await ref.read(profilesProvider.future);
    final dependents = profilesUsingKey(profiles, k.id);
    if (!context.mounted) return;

    if (dependents.isEmpty) {
      await ref.read(keysProvider.notifier).remove(k.id);
      return;
    }

    final ok = await showDialog<bool>(
      context: context,
      builder: (_) => AlertDialog(
        title: Text('Delete "${k.name}"?'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('These profiles will stop working until you assign a new key:'),
            const SizedBox(height: 8),
            for (final p in dependents) Text('• ${p.name}'),
          ],
        ),
        actions: [
          TextButton(
              onPressed: () => Navigator.pop(context, false),
              child: const Text('Cancel')),
          FilledButton(
              key: const Key('key-delete-confirm'),
              onPressed: () => Navigator.pop(context, true),
              child: const Text('Delete')),
        ],
      ),
    );
    if (ok == true) await ref.read(keysProvider.notifier).remove(k.id);
  }
}

class _GenerateTile extends ConsumerStatefulWidget {
  @override
  ConsumerState<_GenerateTile> createState() => _GenerateTileState();
}

class _GenerateTileState extends ConsumerState<_GenerateTile> {
  bool _busy = false;

  void _toast(String msg) =>
      ScaffoldMessenger.of(context).showSnackBar(SnackBar(content: Text(msg)));

  // Prompt for a name (defaulting to the next "Generated key #N") only after the
  // user asks to generate, then create the keypair.
  Future<void> _promptAndGenerate() async {
    // Adding the key rebuilds the list, which can deactivate this (unkeyed) tile
    // mid-await — so capture the Navigator up front (before any await) and open
    // the reveal through it rather than this tile's (possibly dead) context.
    final navigator = Navigator.of(context);
    final names = (ref.read(keysProvider).asData?.value ?? const <LibraryKey>[])
        .map((k) => k.name)
        .toList();
    final name = await showDialog<String>(
      context: context,
      builder: (_) => _NameKeyDialog(initial: nextGeneratedKeyName(names)),
    );
    if (name == null || name.isEmpty) return;
    if (names.contains(name)) {
      _toast('A key named "$name" already exists');
      return;
    }
    setState(() => _busy = true);
    try {
      final g = await generateRsaSshKeyAsync();
      final libKey = LibraryKey(id: newId(), name: name, pem: g.privatePem);
      await ref.read(keysProvider.notifier).add(libKey);
      if (navigator.mounted) {
        navigator.push(DialogRoute<void>(
          context: navigator.context,
          builder: (_) => _PublicKeyDialog(libKey: libKey),
        ));
      }
    } finally {
      if (mounted) setState(() => _busy = false);
    }
  }

  @override
  Widget build(BuildContext context) {
    return ListTile(
      key: const Key('key-generate'),
      leading: const Icon(Icons.add),
      title: const Text('Generate keypair'),
      trailing: _busy
          ? const SizedBox(
              width: 16, height: 16, child: CircularProgressIndicator(strokeWidth: 2))
          : null,
      onTap: _busy ? null : _promptAndGenerate,
    );
  }
}

/// Shows a saved key's OpenSSH public key (derived from the stored private key)
/// with a copy button, so it can be added to a host's `authorized_keys` at any
/// time — not just once at generation.
class _PublicKeyDialog extends StatefulWidget {
  const _PublicKeyDialog({required this.libKey});
  final LibraryKey libKey;

  @override
  State<_PublicKeyDialog> createState() => _PublicKeyDialogState();
}

class _PublicKeyDialogState extends State<_PublicKeyDialog> {
  // Deferred so the dialog paints (with a spinner) before deriving — parsing an
  // encrypted key runs a bcrypt KDF that would otherwise block the first frame.
  late final Future<String> _line = Future(
    () => openSshPublicKeyLine(
        SshKey(widget.libKey.pem, widget.libKey.passphrase),
        comment: widget.libKey.name),
  );

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text(widget.libKey.name),
      content: FutureBuilder<String>(
        future: _line,
        builder: (context, snap) {
          if (snap.connectionState != ConnectionState.done) {
            return const SizedBox(
              height: 48,
              child: Center(child: CircularProgressIndicator()),
            );
          }
          if (snap.hasError) {
            final err = snap.error;
            final msg = err is SshTunnelException ? err.message : '$err';
            return Text(msg, key: const Key('key-public-error'));
          }
          return Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text("Add to the host's ~/.ssh/authorized_keys:"),
              const SizedBox(height: 8),
              SelectableText(snap.data!, key: const Key('key-public-line')),
            ],
          );
        },
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Close'),
        ),
        FutureBuilder<String>(
          future: _line,
          builder: (context, snap) => TextButton(
            key: const Key('key-public-copy'),
            onPressed: snap.hasData
                ? () => Clipboard.setData(ClipboardData(text: snap.data!))
                : null,
            child: const Text('Copy public key'),
          ),
        ),
      ],
    );
  }
}

// A ListTile that opens the import dialog — matches the Generate tile's
// tap-to-open-modal flow.
class _ImportTile extends StatelessWidget {
  const _ImportTile();

  @override
  Widget build(BuildContext context) {
    return ListTile(
      key: const Key('key-import'),
      leading: const Icon(Icons.file_upload_outlined),
      title: const Text('Import key'),
      onTap: () =>
          showDialog(context: context, builder: (_) => const _ImportKeyDialog()),
    );
  }
}

class _ImportKeyDialog extends ConsumerStatefulWidget {
  const _ImportKeyDialog();

  @override
  ConsumerState<_ImportKeyDialog> createState() => _ImportKeyDialogState();
}

class _ImportKeyDialogState extends ConsumerState<_ImportKeyDialog> {
  final _name = TextEditingController();
  final _pem = TextEditingController();
  final _passphrase = TextEditingController();
  bool _showPassphrase = false;
  String? _message; // inline verify/validation feedback

  @override
  void dispose() {
    _name.dispose();
    _pem.dispose();
    _passphrase.dispose();
    super.dispose();
  }

  // The passphrase is optional; treat empty as "no passphrase".
  String? get _pass => _passphrase.text.isEmpty ? null : _passphrase.text;

  void _verify() {
    final pem = _pem.text.trim();
    if (pem.isEmpty) {
      setState(() => _message = 'Paste a key first');
      return;
    }
    setState(() => _message = verifyKey(pem, _pass) ?? 'Key looks good');
  }

  Future<void> _add() async {
    final pem = _pem.text.trim();
    final name = _name.text.trim();
    if (pem.isEmpty || name.isEmpty) {
      setState(() => _message = 'Name and PEM are required');
      return;
    }
    final names =
        (ref.read(keysProvider).asData?.value ?? const []).map((k) => k.name);
    if (names.contains(name)) {
      setState(() => _message = 'A key named "$name" already exists');
      return;
    }
    final err = verifyKey(pem, _pass);
    if (err != null) {
      setState(() => _message = err);
      return;
    }
    await ref
        .read(keysProvider.notifier)
        .add(LibraryKey(id: newId(), name: name, pem: pem, passphrase: _pass));
    if (mounted) Navigator.of(context).pop();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Import key'),
      content: SingleChildScrollView(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            TextField(
              key: const Key('key-import-name'),
              controller: _name,
              decoration: const InputDecoration(labelText: 'Name'),
            ),
            TextField(
              key: const Key('key-import-pem'),
              controller: _pem,
              minLines: 3,
              maxLines: 6,
              decoration: const InputDecoration(labelText: 'OpenSSH PEM'),
            ),
            TextField(
              key: const Key('key-import-passphrase'),
              controller: _passphrase,
              obscureText: !_showPassphrase,
              decoration: InputDecoration(
                labelText: 'Passphrase (if the key is encrypted)',
                suffixIcon: IconButton(
                  key: const Key('key-import-passphrase-visibility'),
                  icon: Icon(
                      _showPassphrase ? Icons.visibility_off : Icons.visibility),
                  tooltip: _showPassphrase ? 'Hide passphrase' : 'Show passphrase',
                  onPressed: () =>
                      setState(() => _showPassphrase = !_showPassphrase),
                ),
              ),
            ),
            if (_message != null)
              Padding(
                padding: const EdgeInsets.only(top: 8),
                child: Text(_message!, key: const Key('key-import-message')),
              ),
          ],
        ),
      ),
      actions: [
        TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('Cancel')),
        OutlinedButton(
          key: const Key('key-verify'),
          onPressed: _verify,
          child: const Text('Verify'),
        ),
        FilledButton(
          key: const Key('key-import-add'),
          onPressed: _add,
          child: const Text('Add'),
        ),
      ],
    );
  }
}

/// Name prompt for a generated key. Owns its controller so it is disposed only
/// after the dialog route is gone (disposing it inline after showDialog throws
/// while the route is still animating out).
class _NameKeyDialog extends StatefulWidget {
  const _NameKeyDialog({required this.initial});
  final String initial;

  @override
  State<_NameKeyDialog> createState() => _NameKeyDialogState();
}

class _NameKeyDialogState extends State<_NameKeyDialog> {
  late final _controller = TextEditingController(text: widget.initial);

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Name the keypair'),
      content: TextField(
        key: const Key('key-generate-name'),
        controller: _controller,
        autofocus: true,
        decoration: const InputDecoration(labelText: 'Name'),
      ),
      actions: [
        TextButton(
            onPressed: () => Navigator.of(context).pop(),
            child: const Text('Cancel')),
        TextButton(
          key: const Key('key-generate-confirm'),
          onPressed: () => Navigator.of(context).pop(_controller.text.trim()),
          child: const Text('Generate'),
        ),
      ],
    );
  }
}
