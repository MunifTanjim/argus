// app/test/pairing/profile_edit_screen_test.dart
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/profile.dart';
import 'package:argus/pairing/profile_edit_screen.dart';
import 'package:argus/state/profiles.dart';
import 'package:argus/transport/key_library_store.dart';
import 'package:argus/transport/library_key.dart';

class MemKv implements SecureKv {
  final m = <String, String>{};
  @override
  Future<String?> read(String key) async => m[key];
  @override
  Future<void> write(String key, String value) async => m[key] = value;
  @override
  Future<void> delete(String key) async => m.remove(key);
}

Future<void> _pump(WidgetTester tester, Widget child,
    {required KeyLibraryStore keys}) async {
  final container = ProviderContainer(overrides: [
    keyLibraryStoreProvider.overrideWithValue(keys),
  ]);
  addTearDown(container.dispose);
  await tester.pumpWidget(UncontrolledProviderScope(
    container: container,
    child: MaterialApp(home: Scaffold(body: child)),
  ));
  await tester.pumpAndSettle();
}

void main() {
  testWidgets('ssh profile picks a library key and emits a Profile', (tester) async {
    final keys = KeyLibraryStore(MemKv());
    await keys.add(const LibraryKey(id: 'k1', name: 'laptop', pem: 'P'));
    Profile? out;

    await _pump(
      tester,
      ProfileEditScreen(
          submitLabel: 'Save', onSubmit: (p) => out = p),
      keys: keys,
    );

    await tester.tap(find.byKey(const Key('mode-ssh')));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('profile-name')), 'prod');
    await tester.enterText(find.byKey(const Key('ssh-host')), 'host.example');
    await tester.enterText(find.byKey(const Key('ssh-user')), 'me');
    await tester.enterText(find.byKey(const Key('ssh-gateway-port')), '8443');
    await tester.enterText(find.byKey(const Key('token')), 'tok');

    // Pick the only library key from the dropdown.
    await tester.ensureVisible(find.byKey(const Key('ssh-key-picker')));
    await tester.tap(find.byKey(const Key('ssh-key-picker')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('laptop').last);
    await tester.pumpAndSettle();

    await tester.ensureVisible(find.byKey(const Key('profile-submit')));
    await tester.tap(find.byKey(const Key('profile-submit')));
    await tester.pumpAndSettle();

    expect(out, isNotNull);
    expect(out!.mode, ProfileMode.ssh);
    expect(out!.name, 'prod');
    expect(out!.host, 'host.example');
    expect(out!.user, 'me');
    expect(out!.gatewayPort, 8443);
    expect(out!.keyId, 'k1');
    expect(out!.token, 'tok');
  });

  testWidgets('invalid gateway port shows an error and does not submit', (tester) async {
    final keys = KeyLibraryStore(MemKv());
    await keys.add(const LibraryKey(id: 'k1', name: 'laptop', pem: 'P'));
    Profile? out;

    await _pump(tester,
        ProfileEditScreen(submitLabel: 'Save', onSubmit: (p) => out = p),
        keys: keys);

    await tester.tap(find.byKey(const Key('mode-ssh')));
    await tester.pumpAndSettle();
    await tester.enterText(find.byKey(const Key('profile-name')), 'prod');
    await tester.enterText(find.byKey(const Key('ssh-host')), 'host.example');
    await tester.enterText(find.byKey(const Key('ssh-gateway-port')), '99999');
    await tester.enterText(find.byKey(const Key('token')), 'tok');
    await tester.ensureVisible(find.byKey(const Key('ssh-key-picker')));
    await tester.tap(find.byKey(const Key('ssh-key-picker')));
    await tester.pumpAndSettle();
    await tester.tap(find.text('laptop').last);
    await tester.pumpAndSettle();

    await tester.ensureVisible(find.byKey(const Key('profile-submit')));
    await tester.tap(find.byKey(const Key('profile-submit')));
    await tester.pumpAndSettle();

    expect(out, isNull);
    expect(find.byKey(const Key('form-error')), findsOneWidget);
  });

  testWidgets('token field is obscured but can be revealed', (tester) async {
    final keys = KeyLibraryStore(MemKv());
    await _pump(tester,
        ProfileEditScreen(submitLabel: 'Save', onSubmit: (_) {}),
        keys: keys);

    TextField tokenField() =>
        tester.widget<TextField>(find.byKey(const Key('token')));
    expect(tokenField().obscureText, isTrue);

    await tester.tap(find.byKey(const Key('token-visibility')));
    await tester.pumpAndSettle();
    expect(tokenField().obscureText, isFalse);

    await tester.tap(find.byKey(const Key('token-visibility')));
    await tester.pumpAndSettle();
    expect(tokenField().obscureText, isTrue);
  });

  testWidgets('editing preserves the id', (tester) async {
    final keys = KeyLibraryStore(MemKv());
    await keys.add(const LibraryKey(id: 'k1', name: 'laptop', pem: 'P'));
    Profile? out;
    const initial = Profile(
        id: 'p-existing', name: 'old', mode: ProfileMode.ssh, token: 'tok',
        host: 'h', gatewayPort: 8443, keyId: 'k1');

    await _pump(tester,
        ProfileEditScreen(initial: initial, submitLabel: 'Save', onSubmit: (p) => out = p),
        keys: keys);

    await tester.enterText(find.byKey(const Key('profile-name')), 'new');
    await tester.ensureVisible(find.byKey(const Key('profile-submit')));
    await tester.tap(find.byKey(const Key('profile-submit')));
    await tester.pumpAndSettle();

    expect(out!.id, 'p-existing');
    expect(out!.name, 'new');
  });

  testWidgets('no delete button when onDelete is null', (tester) async {
    final keys = KeyLibraryStore(MemKv());
    await _pump(tester,
        ProfileEditScreen(submitLabel: 'Save', onSubmit: (_) {}),
        keys: keys);

    expect(find.byKey(const Key('profile-delete')), findsNothing);
  });

  testWidgets('delete button confirms then invokes onDelete', (tester) async {
    final keys = KeyLibraryStore(MemKv());
    var deleted = false;
    const initial = Profile(
        id: 'p1', name: 'prod', mode: ProfileMode.ssh, token: 't',
        host: 'h', gatewayPort: 8443, keyId: 'k1');

    await _pump(
      tester,
      ProfileEditScreen(
        initial: initial,
        submitLabel: 'Save',
        onSubmit: (_) {},
        onDelete: () => deleted = true,
      ),
      keys: keys,
    );

    await tester.ensureVisible(find.byKey(const Key('profile-delete')));
    await tester.tap(find.byKey(const Key('profile-delete')));
    await tester.pumpAndSettle();
    // Confirm dialog is up; deletion has not happened yet.
    expect(deleted, isFalse);
    await tester.tap(find.byKey(const Key('profile-delete-confirm')));
    await tester.pumpAndSettle();
    expect(deleted, isTrue);
  });

  testWidgets('test button validates the form before connecting', (tester) async {
    final keys = KeyLibraryStore(MemKv());
    await _pump(tester,
        ProfileEditScreen(submitLabel: 'Save', onSubmit: (_) {}),
        keys: keys);

    // Empty form: tapping Test should surface the validation error, not crash
    // or attempt a connection.
    await tester.ensureVisible(find.byKey(const Key('profile-test')));
    await tester.tap(find.byKey(const Key('profile-test')));
    await tester.pumpAndSettle();
    expect(find.byKey(const Key('form-error')), findsOneWidget);
  });
}
