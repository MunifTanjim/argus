// app/test/ui/profiles_screen_test.dart
import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/profile.dart';
import 'package:argus/pairing/profile_store.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/state/profiles.dart';
import 'package:argus/transport/key_library_store.dart';
import 'package:argus/transport/library_key.dart';
import 'package:argus/transport/ssh_key_store.dart';
import 'package:argus/ui/profiles_screen.dart';

class MemKv implements SecureKv {
  final m = <String, String>{};
  @override
  Future<String?> read(String key) async => m[key];
  @override
  Future<void> write(String key, String value) async => m[key] = value;
  @override
  Future<void> delete(String key) async => m.remove(key);
}

/// A KV that holds open the active-profile-id write until [gate] completes, so a
/// test can render a frame (swapping the app's home to the connected view and
/// unmounting ProfilesScreen) while the connect flow is still mid-await.
class GatedKv implements SecureKv {
  final m = <String, String>{};
  final gate = Completer<void>();
  @override
  Future<String?> read(String key) async => m[key];
  @override
  Future<void> write(String key, String value) async {
    m[key] = value;
    if (key == 'active_profile_id') await gate.future; // ProfileStore._activeKey
  }

  @override
  Future<void> delete(String key) async => m.remove(key);
}

/// Mirrors main.dart's routing: swaps home ProfilesScreen -> connected view when
/// credentials are set (this is what unmounts ProfilesScreen mid-connect).
class _CredsRoutedApp extends ConsumerWidget {
  const _CredsRoutedApp();
  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final creds = ref.watch(credentialsProvider);
    return MaterialApp(
      home: creds == null
          ? const ProfilesScreen()
          : const Scaffold(body: Center(child: Text('CONNECTED'))),
    );
  }
}

Future<ProviderContainer> _pump(WidgetTester tester,
    {required ProfileStore profiles,
    required KeyLibraryStore keys,
    SshKeyStore? activeKey}) async {
  final container = ProviderContainer(overrides: [
    profileStoreProvider.overrideWithValue(profiles),
    keyLibraryStoreProvider.overrideWithValue(keys),
    sshKeyStoreProvider.overrideWithValue(activeKey ?? SshKeyStore(MemKv())),
  ]);
  addTearDown(container.dispose);
  await tester.pumpWidget(UncontrolledProviderScope(
    container: container,
    child: const MaterialApp(home: ProfilesScreen()),
  ));
  await tester.pumpAndSettle();
  return container;
}

void main() {
  testWidgets('lists profiles and marks a dangling one', (tester) async {
    final profiles = ProfileStore(MemKv());
    await profiles.add(const Profile(
        id: 'p1', name: 'good', mode: ProfileMode.ssh, token: 't', host: 'h', keyId: 'k1'));
    await profiles.add(const Profile(
        id: 'p2', name: 'broken', mode: ProfileMode.ssh, token: 't', host: 'h', keyId: 'gone'));
    final keys = KeyLibraryStore(MemKv());
    await keys.add(const LibraryKey(id: 'k1', name: 'k', pem: 'P'));

    await _pump(tester, profiles: profiles, keys: keys);

    expect(find.text('good'), findsOneWidget);
    expect(find.text('broken'), findsOneWidget);
    expect(find.text('needs a key'), findsOneWidget);
  });

  testWidgets('tapping a healthy profile connects (sets credentials + active key)',
      (tester) async {
    final profiles = ProfileStore(MemKv());
    await profiles.add(const Profile(
      id: 'p1', name: 'good', mode: ProfileMode.ssh, token: 'tok',
      host: 'h', user: 'me', gatewayPort: 8443, keyId: 'k1',
    ));
    final keys = KeyLibraryStore(MemKv());
    await keys.add(const LibraryKey(id: 'k1', name: 'k', pem: 'PEM', passphrase: 'pw'));
    final activeKey = SshKeyStore(MemKv());

    final container =
        await _pump(tester, profiles: profiles, keys: keys, activeKey: activeKey);

    await tester.tap(find.byKey(const Key('profile-p1')));
    await tester.pumpAndSettle();
    // Confirm on the prefilled editor.
    await tester.ensureVisible(find.byKey(const Key('profile-submit')));
    await tester.tap(find.byKey(const Key('profile-submit')));
    await tester.pumpAndSettle();

    final creds = container.read(credentialsProvider);
    expect(creds, isNotNull);
    expect(creds!.url, 'ssh://me@h?port=8443');
    expect((await activeKey.load())!.pem, 'PEM');
  });

  testWidgets(
      'Connect dismisses the connection view even after home swaps to the connected view',
      (tester) async {
    final gatedKv = GatedKv();
    final profiles = ProfileStore(gatedKv);
    await profiles.add(const Profile(
      id: 'p1', name: 'good', mode: ProfileMode.ssh, token: 'tok',
      host: 'h', user: 'me', gatewayPort: 8443, keyId: 'k1',
    ));
    final keys = KeyLibraryStore(MemKv());
    await keys.add(const LibraryKey(id: 'k1', name: 'k', pem: 'PEM', passphrase: 'pw'));

    final container = ProviderContainer(overrides: [
      profileStoreProvider.overrideWithValue(profiles),
      keyLibraryStoreProvider.overrideWithValue(keys),
      sshKeyStoreProvider.overrideWithValue(SshKeyStore(MemKv())),
    ]);
    addTearDown(container.dispose);

    await tester.pumpWidget(UncontrolledProviderScope(
      container: container,
      child: const _CredsRoutedApp(),
    ));
    await tester.pumpAndSettle();

    await tester.tap(find.byKey(const Key('profile-p1')));
    await tester.pumpAndSettle();
    await tester.ensureVisible(find.byKey(const Key('profile-submit')));

    await tester.tap(find.byKey(const Key('profile-submit')));
    // Let credentials get set and the home swap render (which unmounts
    // ProfilesScreen) while the active-id write is still gated.
    await tester.pump();
    await tester.pump();
    // Release the gated write; the connect flow finishes and must dismiss the
    // editor even though ProfilesScreen (its parent context) is now gone.
    gatedKv.gate.complete();
    await tester.pumpAndSettle();

    expect(container.read(credentialsProvider), isNotNull);
    expect(find.byKey(const Key('profile-submit')), findsNothing);
    expect(find.text('CONNECTED'), findsOneWidget);
  });

  testWidgets('shows a crafted empty state with add and scan buttons',
      (tester) async {
    await _pump(tester, profiles: ProfileStore(MemKv()), keys: KeyLibraryStore(MemKv()));

    expect(find.text('No connections yet'), findsOneWidget);
    expect(find.byKey(const Key('profiles-add')), findsOneWidget);
    expect(find.byKey(const Key('profiles-scan')), findsOneWidget);
  });

  testWidgets('shows the argus wordmark header', (tester) async {
    await _pump(tester, profiles: ProfileStore(MemKv()), keys: KeyLibraryStore(MemKv()));

    expect(find.text('◉ argus'), findsOneWidget);
    expect(find.text('watch your agents'), findsOneWidget);
  });

  testWidgets('populated footer shows add and scan', (tester) async {
    final profiles = ProfileStore(MemKv());
    await profiles.add(const Profile(
        id: 'p1', name: 'good', mode: ProfileMode.ssh, token: 't', host: 'h', keyId: 'k1'));
    final keys = KeyLibraryStore(MemKv());
    await keys.add(const LibraryKey(id: 'k1', name: 'k', pem: 'P'));

    await _pump(tester, profiles: profiles, keys: keys);

    expect(find.byKey(const Key('profiles-add')), findsOneWidget);
    expect(find.byKey(const Key('profiles-scan')), findsOneWidget);
  });

  testWidgets('row edit button opens the editor', (tester) async {
    final profiles = ProfileStore(MemKv());
    await profiles.add(const Profile(
        id: 'p1', name: 'good', mode: ProfileMode.ssh, token: 't', host: 'h', keyId: 'k1'));
    final keys = KeyLibraryStore(MemKv());
    await keys.add(const LibraryKey(id: 'k1', name: 'k', pem: 'P'));

    await _pump(tester, profiles: profiles, keys: keys);

    await tester.tap(find.byKey(const Key('profile-edit-p1')));
    await tester.pumpAndSettle();
    // The editor is up (its submit button is present) with a delete action.
    expect(find.byKey(const Key('profile-submit')), findsOneWidget);
    expect(find.byKey(const Key('profile-delete')), findsOneWidget);
  });

  testWidgets('dot color reflects the connection test result', (tester) async {
    final profiles = ProfileStore(MemKv());
    await profiles.add(const Profile(
        id: 'p1', name: 'good', mode: ProfileMode.ssh, token: 't', host: 'h', keyId: 'k1'));
    final keys = KeyLibraryStore(MemKv());
    await keys.add(const LibraryKey(id: 'k1', name: 'k', pem: 'P'));

    final container = await _pump(tester, profiles: profiles, keys: keys);

    Color dot() => (tester
            .widget<Container>(find.byKey(const Key('profile-dot-p1')))
            .decoration as BoxDecoration)
        .color!;

    // Untested → grey (AppColors.dim).
    expect(dot(), const Color(0xFF928374));

    container.read(connectionTestResultsProvider.notifier).set('p1', false);
    await tester.pump();
    expect(dot(), const Color(0xFFfb4934)); // red

    container.read(connectionTestResultsProvider.notifier).set('p1', true);
    await tester.pump();
    expect(dot(), const Color(0xFF8ec07c)); // AppColors.accent green
  });
}
