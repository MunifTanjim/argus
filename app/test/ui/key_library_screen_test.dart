import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/profile.dart';
import 'package:argus/pairing/profile_store.dart';
import 'package:argus/state/profiles.dart';
import 'package:argus/transport/key_library_store.dart';
import 'package:argus/transport/library_key.dart';
import 'package:argus/ui/key_library_screen.dart';

class MemKv implements SecureKv {
  final m = <String, String>{};
  @override
  Future<String?> read(String key) async => m[key];
  @override
  Future<void> write(String key, String value) async => m[key] = value;
  @override
  Future<void> delete(String key) async => m.remove(key);
}

Future<ProviderContainer> _pump(WidgetTester tester,
    {required KeyLibraryStore keys, required ProfileStore profiles}) async {
  final container = ProviderContainer(overrides: [
    keyLibraryStoreProvider.overrideWithValue(keys),
    profileStoreProvider.overrideWithValue(profiles),
  ]);
  addTearDown(container.dispose);
  await tester.pumpWidget(UncontrolledProviderScope(
    container: container,
    child: const MaterialApp(home: KeyLibraryScreen()),
  ));
  await tester.pumpAndSettle();
  return container;
}

void main() {
  testWidgets('lists existing keys', (tester) async {
    final keys = KeyLibraryStore(MemKv());
    await keys.add(const LibraryKey(id: 'k1', name: 'laptop', pem: 'P'));
    await _pump(tester, keys: keys, profiles: ProfileStore(MemKv()));
    expect(find.text('laptop'), findsOneWidget);
  });

  testWidgets('deleting an unreferenced key removes it without a warning', (tester) async {
    final keys = KeyLibraryStore(MemKv());
    await keys.add(const LibraryKey(id: 'k1', name: 'laptop', pem: 'P'));
    final container = await _pump(tester, keys: keys, profiles: ProfileStore(MemKv()));

    await tester.tap(find.byKey(const Key('key-delete-k1')));
    await tester.pumpAndSettle();

    expect(await container.read(keysProvider.future), isEmpty);
  });

  testWidgets('deleting a referenced key warns then cascades', (tester) async {
    final keys = KeyLibraryStore(MemKv());
    await keys.add(const LibraryKey(id: 'k1', name: 'laptop', pem: 'P'));
    final profiles = ProfileStore(MemKv());
    await profiles.add(const Profile(
        id: 'p1', name: 'prod-box', mode: ProfileMode.ssh, token: 't', host: 'h', keyId: 'k1'));
    final container = await _pump(tester, keys: keys, profiles: profiles);

    await tester.tap(find.byKey(const Key('key-delete-k1')));
    await tester.pumpAndSettle();
    expect(find.textContaining('prod-box'), findsWidgets); // dialog lists the dependent profile

    await tester.tap(find.byKey(const Key('key-delete-confirm')));
    await tester.pumpAndSettle();

    expect(await container.read(keysProvider.future), isEmpty);
  });

  testWidgets('import shows a passphrase field with a reveal toggle',
      (tester) async {
    await _pump(tester, keys: KeyLibraryStore(MemKv()), profiles: ProfileStore(MemKv()));
    await tester.tap(find.text('Import key'));
    await tester.pumpAndSettle();

    expect(find.byKey(const Key('key-import-passphrase')), findsOneWidget);
    final field = tester.widget<TextField>(find.byKey(const Key('key-import-passphrase')));
    expect(field.obscureText, isTrue);
    await tester.tap(find.byKey(const Key('key-import-passphrase-visibility')));
    await tester.pumpAndSettle();
    expect(
      tester.widget<TextField>(find.byKey(const Key('key-import-passphrase'))).obscureText,
      isFalse,
    );
  });

  testWidgets('import rejects an invalid key instead of saving it', (tester) async {
    final keys = KeyLibraryStore(MemKv());
    await _pump(tester, keys: keys, profiles: ProfileStore(MemKv()));
    await tester.tap(find.text('Import key'));
    await tester.pumpAndSettle();

    await tester.enterText(find.byKey(const Key('key-import-name')), 'bad');
    await tester.enterText(find.byKey(const Key('key-import-pem')), 'not a key');
    await tester.ensureVisible(find.byKey(const Key('key-import-add')));
    await tester.tap(find.byKey(const Key('key-import-add')));
    await tester.pumpAndSettle();

    expect(await keys.list(), isEmpty);
  });

  testWidgets('generate prompts for a name defaulting to #1', (tester) async {
    await _pump(tester, keys: KeyLibraryStore(MemKv()), profiles: ProfileStore(MemKv()));
    // The name field appears only after tapping Generate.
    expect(find.byKey(const Key('key-generate-name')), findsNothing);
    await tester.tap(find.byKey(const Key('key-generate')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('key-generate-name')), findsOneWidget);
    expect(find.text('Generated key #1'), findsOneWidget);

    // Cancelling must tear down cleanly (no controller-disposed assertion).
    await tester.tap(find.text('Cancel'));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('key-generate-name')), findsNothing);
  });

  testWidgets('import rejects a duplicate name', (tester) async {
    final keys = KeyLibraryStore(MemKv());
    await keys.add(const LibraryKey(id: 'k1', name: 'dup', pem: 'P'));
    await _pump(tester, keys: keys, profiles: ProfileStore(MemKv()));
    await tester.tap(find.text('Import key'));
    await tester.pumpAndSettle();

    await tester.enterText(find.byKey(const Key('key-import-name')), 'dup');
    await tester.enterText(find.byKey(const Key('key-import-pem')), 'whatever');
    await tester.ensureVisible(find.byKey(const Key('key-import-add')));
    await tester.tap(find.byKey(const Key('key-import-add')));
    await tester.pumpAndSettle();

    expect((await keys.list()).where((k) => k.name == 'dup').length, 1);
  });
}
