import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/pairing_uri.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/transport/ssh_hostkey_store.dart';
import 'package:argus/ui/settings_screen.dart';

class MemKv implements SecureKv {
  final _m = <String, String>{};
  @override
  Future<String?> read(String key) async => _m[key];
  @override
  Future<void> write(String key, String value) async => _m[key] = value;
  @override
  Future<void> delete(String key) async => _m.remove(key);
}

Widget _app({
  required GatewayCredentials? creds,
  required HostKeyStore hostKeyStore,
}) {
  return ProviderScope(
    overrides: [
      credentialsProvider.overrideWith((ref) => creds),
      hostKeyStoreProvider.overrideWithValue(hostKeyStore),
      gatewayProvider.overrideWithValue(null),
      connStateProvider.overrideWith((ref) => ConnState.disconnected),
    ],
    child: const MaterialApp(home: SettingsScreen()),
  );
}

void main() {
  testWidgets(
      'ssh:// gateway shows forget-host-key button and forgetting clears the pin',
      (tester) async {
    const sshUrl = 'ssh://me@host.example:22?port=8443';
    const creds = GatewayCredentials(sshUrl, 'tok');
    final hostKeyStore = HostKeyStore(MemKv());
    // Pre-pin a fingerprint for the host.
    await hostKeyStore.pin('host.example:22', 'ssh-ed25519', 'SHA256:ABCD');
    expect(await hostKeyStore.pinned('host.example:22', 'ssh-ed25519'),
        'SHA256:ABCD');

    await tester.pumpWidget(_app(creds: creds, hostKeyStore: hostKeyStore));
    await tester.pump();

    // The forget-host-key button must be visible for an ssh:// gateway.
    expect(find.byKey(const Key('forget-host-key')), findsOneWidget);

    // Tap it.
    await tester.tap(find.byKey(const Key('forget-host-key')));
    await tester.pumpAndSettle();

    // The pin must have been cleared.
    expect(await hostKeyStore.pinned('host.example:22', 'ssh-ed25519'), isNull);

    // A confirmation snackbar must be shown.
    expect(
      find.textContaining('Forgot host key for host.example:22'),
      findsOneWidget,
    );
  });

  testWidgets('wss:// gateway hides the forget-host-key button', (tester) async {
    const wssUrl = 'wss://gateway.example/ws';
    const creds = GatewayCredentials(wssUrl, 'tok');
    final hostKeyStore = HostKeyStore(MemKv());

    await tester.pumpWidget(_app(creds: creds, hostKeyStore: hostKeyStore));
    await tester.pump();

    expect(find.byKey(const Key('forget-host-key')), findsNothing);
  });
}
