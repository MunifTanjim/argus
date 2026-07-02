// app/lib/pairing/profile_edit_screen.dart
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../state/gateway.dart';
import '../state/profiles.dart';
import '../transport/ssh_gateway.dart';
import '../ui/responsive.dart';
import '../util/id.dart';
import 'profile.dart';

/// Create/edit a connection profile, or review one before connecting. The SSH
/// key is chosen from the library (never pasted here). Pure form: it emits a
/// Profile via [onSubmit]; the caller persists and/or connects.
class ProfileEditScreen extends ConsumerStatefulWidget {
  const ProfileEditScreen({
    super.key,
    this.initial,
    required this.submitLabel,
    required this.onSubmit,
    this.onDelete,
  });

  final Profile? initial;
  final String submitLabel;
  final void Function(Profile) onSubmit;
  final VoidCallback? onDelete;

  @override
  ConsumerState<ProfileEditScreen> createState() => _ProfileEditScreenState();
}

class _ProfileEditScreenState extends ConsumerState<ProfileEditScreen> {
  late ProfileMode _mode;
  bool _showToken = false;
  bool _testing = false;
  String? _keyId;
  String? _error;

  late final _name = TextEditingController(text: widget.initial?.name ?? '');
  late final _token = TextEditingController(text: widget.initial?.token ?? '');
  late final _url = TextEditingController(text: widget.initial?.url ?? '');
  late final _host = TextEditingController(text: widget.initial?.host ?? '');
  late final _user = TextEditingController(text: widget.initial?.user ?? '');
  late final _sshPort =
      TextEditingController(text: widget.initial?.sshPort?.toString() ?? '');
  late final _gatewayPort = TextEditingController(
      text: (widget.initial?.gatewayPort ?? kDefaultGatewayPort).toString());

  @override
  void initState() {
    super.initState();
    _mode = widget.initial?.mode ?? ProfileMode.ssh;
    _keyId = widget.initial?.keyId;
  }

  @override
  void dispose() {
    for (final c in [_name, _token, _url, _host, _user, _sshPort, _gatewayPort]) {
      c.dispose();
    }
    super.dispose();
  }

  Profile? _validatedProfile() {
    setState(() => _error = null);
    final name = _name.text.trim();
    final token = _token.text.trim();
    if (name.isEmpty || token.isEmpty) {
      setState(() => _error = 'Name and token are required');
      return null;
    }
    final id = widget.initial?.id ?? newId();

    if (_mode == ProfileMode.direct) {
      final url = _url.text.trim();
      if (url.isEmpty) {
        setState(() => _error = 'Gateway URL is required');
        return null;
      }
      return Profile(
          id: id, name: name, mode: ProfileMode.direct, token: token, url: url);
    }

    final host = _host.text.trim();
    if (host.isEmpty) {
      setState(() => _error = 'SSH host is required');
      return null;
    }
    if (_keyId == null) {
      setState(() => _error = 'Pick an SSH key');
      return null;
    }
    final int gatewayPort;
    final int? sshPort;
    try {
      gatewayPort = _gatewayPort.text.trim().isEmpty
          ? kDefaultGatewayPort
          : parsePort(_gatewayPort.text, source: 'gateway port');
      sshPort = _sshPort.text.trim().isEmpty
          ? null
          : parsePort(_sshPort.text, source: 'ssh port');
    } on FormatException catch (e) {
      setState(() => _error = e.message);
      return null;
    }
    final user = _user.text.trim();
    return Profile(
      id: id,
      name: name,
      mode: ProfileMode.ssh,
      token: token,
      host: host,
      user: user.isEmpty ? null : user,
      sshPort: sshPort,
      gatewayPort: gatewayPort,
      keyId: _keyId,
    );
  }

  void _submit() {
    final p = _validatedProfile();
    if (p != null) widget.onSubmit(p);
  }

  Future<void> _testConnection() async {
    final p = _validatedProfile();
    if (p == null) return;
    setState(() => _testing = true);
    try {
      final key = p.keyId == null
          ? null
          : await ref.read(keyLibraryStoreProvider).get(p.keyId!);
      final err =
          await verifyConnection(p, key, ref.read(hostKeyStoreProvider));
      ref.read(connectionTestResultsProvider.notifier).set(p.id, err == null);
      if (!mounted) return;
      ScaffoldMessenger.of(context)
          .showSnackBar(SnackBar(content: Text(err ?? 'Connection OK')));
    } finally {
      if (mounted) setState(() => _testing = false);
    }
  }

  Future<void> _confirmDelete() async {
    final name = widget.initial?.name ?? 'this connection';
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text('Delete $name?'),
        content: const Text('This removes the connection. Your SSH keys are kept.'),
        actions: [
          TextButton(
              onPressed: () => Navigator.of(ctx).pop(false),
              child: const Text('Cancel')),
          TextButton(
            key: const Key('profile-delete-confirm'),
            style: TextButton.styleFrom(
                foregroundColor: Theme.of(ctx).colorScheme.error),
            onPressed: () => Navigator.of(ctx).pop(true),
            child: const Text('Delete'),
          ),
        ],
      ),
    );
    if (ok == true) widget.onDelete?.call();
  }

  @override
  Widget build(BuildContext context) {
    final keys = ref.watch(keysProvider);
    return Scaffold(
      appBar: AppBar(title: Text(widget.initial == null ? 'New connection' : 'Edit connection')),
      body: CenteredBody(
        maxWidth: 480,
        child: Padding(
          // Scaffold.resizeToAvoidBottomInset already shrinks the body for the
          // keyboard; adding viewInsets here too would double-count and push the
          // focused field off-screen.
          padding: const EdgeInsets.all(16),
          child: SingleChildScrollView(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              children: [
                SegmentedButton<ProfileMode>(
                  segments: const [
                    ButtonSegment(
                        value: ProfileMode.direct,
                        label: Text('Direct', key: Key('mode-direct')),
                        icon: Icon(Icons.link)),
                    ButtonSegment(
                        value: ProfileMode.ssh,
                        label: Text('SSH', key: Key('mode-ssh')),
                        icon: Icon(Icons.terminal)),
                  ],
                  selected: {_mode},
                  onSelectionChanged: (s) => setState(() { _mode = s.first; _error = null; }),
                ),
                const SizedBox(height: 16),
                TextField(
                  key: const Key('profile-name'),
                  controller: _name,
                  decoration: const InputDecoration(labelText: 'Name'),
                ),
                if (_mode == ProfileMode.direct)
                  TextField(
                    key: const Key('url'),
                    controller: _url,
                    decoration: const InputDecoration(labelText: 'Gateway URL'),
                  )
                else ...[
                  TextField(
                    key: const Key('ssh-host'),
                    controller: _host,
                    decoration: const InputDecoration(labelText: 'SSH host'),
                  ),
                  TextField(
                    key: const Key('ssh-user'),
                    controller: _user,
                    decoration: const InputDecoration(
                        labelText: 'SSH user (optional)', helperText: 'Defaults to root'),
                  ),
                  TextField(
                    key: const Key('ssh-port'),
                    controller: _sshPort,
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(
                        labelText: 'SSH port (optional, default 22)'),
                  ),
                  TextField(
                    key: const Key('ssh-gateway-port'),
                    controller: _gatewayPort,
                    keyboardType: TextInputType.number,
                    decoration: const InputDecoration(labelText: 'Gateway port'),
                  ),
                  const SizedBox(height: 8),
                  keys.maybeWhen(
                    data: (list) => DropdownButtonFormField<String>(
                      key: const Key('ssh-key-picker'),
                      initialValue: list.any((k) => k.id == _keyId) ? _keyId : null,
                      decoration: const InputDecoration(labelText: 'SSH key'),
                      items: [
                        for (final k in list)
                          DropdownMenuItem(value: k.id, child: Text(k.name)),
                      ],
                      onChanged: (v) => setState(() => _keyId = v),
                    ),
                    orElse: () => const LinearProgressIndicator(),
                  ),
                ],
                TextField(
                  key: const Key('token'),
                  controller: _token,
                  obscureText: !_showToken,
                  decoration: InputDecoration(
                    labelText: 'Token',
                    suffixIcon: IconButton(
                      key: const Key('token-visibility'),
                      icon: Icon(
                          _showToken ? Icons.visibility_off : Icons.visibility),
                      tooltip: _showToken ? 'Hide token' : 'Show token',
                      onPressed: () => setState(() => _showToken = !_showToken),
                    ),
                  ),
                ),
                if (_error != null) ...[
                  const SizedBox(height: 12),
                  Text(_error!,
                      key: const Key('form-error'),
                      style: TextStyle(color: Theme.of(context).colorScheme.error)),
                ],
                const SizedBox(height: 16),
                FilledButton(
                  key: const Key('profile-submit'),
                  onPressed: _submit,
                  child: Text(widget.submitLabel),
                ),
                const SizedBox(height: 8),
                OutlinedButton(
                  key: const Key('profile-test'),
                  onPressed: _testing ? null : _testConnection,
                  child: _testing
                      ? const SizedBox(
                          height: 16,
                          width: 16,
                          child: CircularProgressIndicator(strokeWidth: 2))
                      : const Text('Test connection'),
                ),
                if (widget.onDelete != null) ...[
                  const SizedBox(height: 8),
                  TextButton(
                    key: const Key('profile-delete'),
                    style: TextButton.styleFrom(
                        foregroundColor: Theme.of(context).colorScheme.error),
                    onPressed: _confirmDelete,
                    child: const Text('Delete connection'),
                  ),
                ],
              ],
            ),
          ),
        ),
      ),
    );
  }
}
