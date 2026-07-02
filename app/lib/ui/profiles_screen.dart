// app/lib/ui/profiles_screen.dart
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../pairing/pairing_uri.dart';
import '../pairing/profile.dart';
import '../pairing/profile_edit_screen.dart';
import '../pairing/scan_screen.dart';
import '../state/gateway.dart';
import '../state/profiles.dart';
import '../util/id.dart';
import 'key_library_screen.dart';
import 'responsive.dart';
import 'theme.dart';

// Amber marks a profile whose SSH key is missing (dangling); the row dot
// otherwise reflects the last connection-test result (red/green) or grey when
// untested this session.
const _danglingDot = Color(0xFFd79921);

class ProfilesScreen extends ConsumerWidget {
  const ProfilesScreen({super.key});

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final profiles = ref.watch(profilesProvider);
    final keys = ref.watch(keysProvider);
    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            _LandingHeader(
              onKeys: () => Navigator.of(context).push(
                  MaterialPageRoute(builder: (_) => const KeyLibraryScreen())),
            ),
            Expanded(
              child: profiles.when(
                loading: () => const Center(child: CircularProgressIndicator()),
                error: (e, _) => Center(child: Text('$e')),
                data: (list) {
                  final keyList = keys.asData?.value ?? const [];
                  final results = ref.watch(connectionTestResultsProvider);
                  if (list.isEmpty) {
                    return _EmptyConnections(
                      onAdd: () => _edit(context, ref, null),
                      onScan: () => _scan(context, ref),
                    );
                  }
                  return CenteredBody(
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.stretch,
                      children: [
                        const Padding(
                          padding: EdgeInsets.fromLTRB(16, 8, 16, 4),
                          child: Text(
                            '▌ CONNECTIONS',
                            style: TextStyle(
                              fontFamily: 'monospace',
                              fontSize: 12,
                              letterSpacing: 1,
                              color: AppColors.dim,
                            ),
                          ),
                        ),
                        Expanded(
                          child: ListView(
                            children: [
                              for (final p in list)
                                _connectionRow(context, ref, p,
                                    profileIsDangling(p, keyList), results[p.id]),
                            ],
                          ),
                        ),
                        Padding(
                          padding: const EdgeInsets.all(16),
                          child: _AddScanButtons(
                            onAdd: () => _edit(context, ref, null),
                            onScan: () => _scan(context, ref),
                          ),
                        ),
                      ],
                    ),
                  );
                },
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _connectionRow(BuildContext context, WidgetRef ref, Profile p,
      bool dangling, bool? testOk) {
    return _ConnectionRow(
      profile: p,
      dangling: dangling,
      testOk: testOk,
      onTap: () => dangling
          ? _edit(context, ref, p, deletable: true)
          : _connect(context, ref, p),
      onEdit: () => _edit(context, ref, p, deletable: true),
    );
  }

  Future<void> _connect(BuildContext context, WidgetRef ref, Profile p) async {
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => ProfileEditScreen(
        initial: p,
        submitLabel: 'Connect',
        onSubmit: (edited) async {
          await ref.read(profilesProvider.notifier).save(edited);
          ref.read(connectionTestResultsProvider.notifier).clear(edited.id);
          final creds = await activateProfile(
            edited,
            ref.read(keyLibraryStoreProvider),
            ref.read(sshKeyStoreProvider),
          );
          ref.read(credentialsProvider.notifier).state = creds;
          await ref.read(profileStoreProvider).saveActiveId(edited.id);
          if (context.mounted) Navigator.of(context).pop();
        },
      ),
    ));
  }

  Future<void> _edit(BuildContext context, WidgetRef ref, Profile? p,
      {bool deletable = false}) async {
    await Navigator.of(context).push(MaterialPageRoute(
      builder: (_) => ProfileEditScreen(
        initial: p,
        submitLabel: 'Save',
        onSubmit: (edited) async {
          await ref.read(profilesProvider.notifier).save(edited);
          ref.read(connectionTestResultsProvider.notifier).clear(edited.id);
          if (context.mounted) Navigator.of(context).pop();
        },
        onDelete: (deletable && p != null)
            ? () async {
                await ref.read(profilesProvider.notifier).remove(p.id);
                ref.read(connectionTestResultsProvider.notifier).clear(p.id);
                if (context.mounted) Navigator.of(context).pop();
              }
            : null,
      ),
    ));
  }

  Future<void> _scan(BuildContext context, WidgetRef ref) async {
    final c = await Navigator.of(context).push<GatewayCredentials>(
        MaterialPageRoute(builder: (_) => const ScanScreen()));
    if (c == null || !context.mounted) return;
    await _edit(context, ref, draftFromCredentials(newId(), c));
  }
}

class _LandingHeader extends StatelessWidget {
  const _LandingHeader({required this.onKeys});
  final VoidCallback onKeys;

  @override
  Widget build(BuildContext context) {
    return SizedBox(
      height: 96,
      child: Stack(
        children: [
          Align(
            alignment: Alignment.topRight,
            child: IconButton(
              key: const Key('profiles-keys'),
              icon: const Icon(Icons.vpn_key_outlined, color: AppColors.dim),
              tooltip: 'SSH keys',
              onPressed: onKeys,
            ),
          ),
          const Align(
            alignment: Alignment.topCenter,
            child: Padding(
              padding: EdgeInsets.fromLTRB(0, 22, 0, 0),
              child: Column(
                mainAxisSize: MainAxisSize.min,
                children: [
                  Text(
                    '◉ argus',
                    style: TextStyle(
                      fontFamily: 'monospace',
                      fontSize: 20,
                      letterSpacing: 2,
                      color: AppColors.accent,
                    ),
                  ),
                  SizedBox(height: 4),
                  Text(
                    'watch your agents',
                    style: TextStyle(fontSize: 12, color: AppColors.dim),
                  ),
                ],
              ),
            ),
          ),
        ],
      ),
    );
  }
}

class _ConnectionRow extends StatelessWidget {
  const _ConnectionRow({
    required this.profile,
    required this.dangling,
    required this.testOk,
    required this.onTap,
    required this.onEdit,
  });
  final Profile profile;
  final bool dangling;
  final bool? testOk;
  final VoidCallback onTap;
  final VoidCallback onEdit;

  // amber = needs a key; red = last test failed; green = last test passed;
  // grey = untested this session.
  Color get _dotColor {
    if (dangling) return _danglingDot;
    if (testOk == false) return const Color(0xFFfb4934);
    if (testOk == true) return AppColors.accent;
    return AppColors.dim;
  }

  @override
  Widget build(BuildContext context) {
    return ListTile(
      key: Key('profile-${profile.id}'),
      leading: SizedBox(
        width: 12,
        child: Center(
          child: Container(
            key: Key('profile-dot-${profile.id}'),
            width: 10,
            height: 10,
            decoration: BoxDecoration(shape: BoxShape.circle, color: _dotColor),
          ),
        ),
      ),
      title: Text(profile.name, style: const TextStyle(color: AppColors.text)),
      subtitle: Text(
        dangling ? 'needs a key' : (profile.host ?? profile.url ?? ''),
        style: const TextStyle(color: AppColors.dim),
      ),
      trailing: IconButton(
        key: Key('profile-edit-${profile.id}'),
        icon: const Icon(Icons.edit_outlined, color: AppColors.dim),
        tooltip: 'Edit',
        onPressed: onEdit,
      ),
      onTap: onTap,
    );
  }
}

class _AddScanButtons extends StatelessWidget {
  const _AddScanButtons({required this.onAdd, required this.onScan});
  final VoidCallback onAdd;
  final VoidCallback onScan;

  @override
  Widget build(BuildContext context) {
    return Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        FilledButton.icon(
          key: const Key('profiles-add'),
          style: FilledButton.styleFrom(
            backgroundColor: AppColors.accent,
            foregroundColor: AppColors.canvas,
          ),
          onPressed: onAdd,
          icon: const Icon(Icons.add),
          label: const Text('Add connection'),
        ),
        const SizedBox(height: 10),
        OutlinedButton.icon(
          key: const Key('profiles-scan'),
          onPressed: onScan,
          icon: const Icon(Icons.qr_code_scanner),
          label: const Text('Scan QR code'),
        ),
      ],
    );
  }
}

class _EmptyConnections extends StatelessWidget {
  const _EmptyConnections({required this.onAdd, required this.onScan});
  final VoidCallback onAdd;
  final VoidCallback onScan;

  @override
  Widget build(BuildContext context) {
    return CenteredBody(
      maxWidth: 420,
      child: Padding(
        padding: const EdgeInsets.all(24),
        child: Column(
          mainAxisAlignment: MainAxisAlignment.center,
          children: [
            Container(
              width: 64,
              height: 64,
              decoration: BoxDecoration(
                shape: BoxShape.circle,
                border: Border.all(color: AppColors.border),
              ),
              child: const Icon(Icons.radio_button_unchecked, color: AppColors.dim),
            ),
            const SizedBox(height: 16),
            const Text('No connections yet',
                style: TextStyle(fontSize: 16, color: AppColors.text)),
            const SizedBox(height: 6),
            const Text(
              'Add your first argus host to start watching your Claude Code sessions.',
              textAlign: TextAlign.center,
              style: TextStyle(color: AppColors.dim, height: 1.4),
            ),
            const SizedBox(height: 24),
            SizedBox(
              width: double.infinity,
              child: _AddScanButtons(onAdd: onAdd, onScan: onScan),
            ),
          ],
        ),
      ),
    );
  }
}
