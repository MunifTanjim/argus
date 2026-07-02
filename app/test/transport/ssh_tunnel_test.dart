import 'package:flutter_test/flutter_test.dart';
import 'package:argus/transport/connection.dart';
import 'package:argus/transport/ssh_key_store.dart';
import 'package:argus/transport/ssh_tunnel.dart';
import 'package:argus/transport/ssh_keygen.dart';

void main() {
  test('parseIdentities throws SshTunnelException on invalid pem', () {
    expect(() => parseIdentities(const SshKey('not a private key')),
        throwsA(isA<SshTunnelException>()));
  });

  group('authFailureException', () {
    // A rejected pinned host key is a possible MITM and must surface a distinct,
    // actionable message rather than a generic auth failure — this is the
    // security-critical branch in SshTunnel.open's auth error handling.
    test('flags a rejected host key as a possible MITM', () {
      final e = authFailureException('host:22',
          hostKeyRejected: true, cause: 'handshake aborted');
      expect(e.message, contains('host key changed for host:22'));
      expect(e.message, contains('MITM'));
      expect(e.message, contains('forget'));
      // Must not leak the raw cause as if it were an ordinary auth failure.
      expect(e.message, isNot(contains('handshake aborted')));
      // Must be fatal so ConnectionManager stops redialing and surfaces it,
      // rather than looping silently in backoff.
      expect(e, isA<FatalConnectError>());
      expect(e, isA<HostKeyChangedException>());
    });

    test('reports an ordinary auth failure with its cause', () {
      final e = authFailureException('host:22',
          hostKeyRejected: false, cause: 'permission denied');
      expect(e.message, contains('SSH authentication failed'));
      expect(e.message, contains('permission denied'));
      expect(e.message, isNot(contains('MITM')));
    });
  });

  test('authTimeoutException reports a timeout, not an auth failure', () {
    final e = authTimeoutException('host:22', const Duration(seconds: 15));
    expect(e.message, contains('timed out after 15s'));
    expect(e.message, contains('host:22'));
    expect(e.message, isNot(contains('authentication failed')));
  });

  test('verifyKey returns null for a valid unencrypted key', () {
    final pem = generateRsaSshKey(bits: 1024).privatePem;
    expect(verifyKey(pem, null), isNull);
  });

  test('verifyKey returns a message for garbage input', () {
    final msg = verifyKey('not a private key', null);
    expect(msg, isNotNull);
    expect(msg, contains('invalid SSH key'));
  });
}
