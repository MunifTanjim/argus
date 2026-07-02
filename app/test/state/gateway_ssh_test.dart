import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/pairing/pairing_uri.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/transport/ssh_hostkey_store.dart';
import 'package:argus/transport/ssh_key_store.dart';
import 'package:argus/transport/ssh_tunnel.dart';

class MemKv implements SecureKv {
  final _m = <String, String>{};
  @override
  Future<String?> read(String key) async => _m[key];
  @override
  Future<void> write(String key, String value) async => _m[key] = value;
  @override
  Future<void> delete(String key) async => _m.remove(key);
}

void main() {
  test('ssh scheme with no stored key throws StateError', () async {
    final keyStore = SshKeyStore(MemKv());
    final hostKeys = HostKeyStore(MemKv());
    await expectLater(
      connectForCredentials(
          const GatewayCredentials('ssh://h?port=8443', 'tok'),
          keyStore,
          hostKeys),
      throwsA(isA<StateError>()),
    );
  });

  test('ssh scheme with an (invalid) stored key takes the ssh path and fails at key parse, not the network', () async {
    final keyStore = SshKeyStore(MemKv());
    await keyStore.save(const SshKey('not a valid pem'));
    final hostKeys = HostKeyStore(MemKv());
    await expectLater(
      connectForCredentials(
          const GatewayCredentials('ssh://h?port=8443', 'tok'),
          keyStore,
          hostKeys),
      throwsA(isA<SshTunnelException>()),
    );
  });
}
