import 'package:flutter_test/flutter_test.dart';
import 'package:argus/transport/ssh_gateway.dart';

void main() {
  group('isSshGatewayUrl', () {
    test('true only for ssh scheme', () {
      expect(isSshGatewayUrl('ssh://host'), isTrue);
      expect(isSshGatewayUrl('wss://host'), isFalse);
      expect(isSshGatewayUrl('ws://host'), isFalse);
      expect(isSshGatewayUrl('nonsense'), isFalse);
    });
  });

  group('parseSshGatewayUrl', () {
    test('parses user, ssh port, and gateway port', () {
      final c = parseSshGatewayUrl('ssh://me@host.example:2222?port=9000');
      expect(c.host, 'host.example');
      expect(c.user, 'me');
      expect(c.sshPort, 2222);
      expect(c.gatewayPort, 9000);
    });

    test('defaults: no user, ssh port null (=>22), gateway port 8443', () {
      final c = parseSshGatewayUrl('ssh://host.example');
      expect(c.host, 'host.example');
      expect(c.user, isNull);
      expect(c.sshPort, isNull);
      expect(c.gatewayPort, 8443);
    });

    test('rejects non-ssh scheme, missing host, and any path', () {
      expect(() => parseSshGatewayUrl('wss://host'), throwsFormatException);
      expect(() => parseSshGatewayUrl('ssh://'), throwsFormatException);
      expect(() => parseSshGatewayUrl('ssh://host/x'), throwsFormatException);
    });

    test('rejects a non-integer gateway port', () {
      expect(() => parseSshGatewayUrl('ssh://host?port=abc'), throwsFormatException);
    });

    test('rejects out-of-range gateway ports', () {
      expect(() => parseSshGatewayUrl('ssh://host?port=0'), throwsFormatException);
      expect(() => parseSshGatewayUrl('ssh://host?port=-5'), throwsFormatException);
      expect(
          () => parseSshGatewayUrl('ssh://host?port=99999'), throwsFormatException);
      // Non-decimal must not be silently reinterpreted (0x10 -> 16).
      expect(
          () => parseSshGatewayUrl('ssh://host?port=0x10'), throwsFormatException);
    });

    test('rejects an out-of-range ssh port', () {
      expect(
          () => parseSshGatewayUrl('ssh://host:99999'), throwsFormatException);
    });
  });

  group('parsePort', () {
    test('accepts the valid range boundaries', () {
      expect(parsePort('1'), 1);
      expect(parsePort('65535'), 65535);
      expect(parsePort(' 8443 '), 8443);
    });

    test('rejects zero, negative, overflow, hex, and non-numeric', () {
      for (final bad in ['0', '-1', '65536', '0x10', 'abc', '']) {
        expect(() => parsePort(bad), throwsFormatException, reason: bad);
      }
    });
  });

  group('buildSshGatewayUrl / round-trip', () {
    test('builds a url that parses back to the same config', () {
      final url = buildSshGatewayUrl(
          host: 'h', user: 'u', sshPort: 2222, gatewayPort: 9000);
      expect(url, 'ssh://u@h:2222?port=9000');
      final c = parseSshGatewayUrl(url);
      expect(c.host, 'h');
      expect(c.user, 'u');
      expect(c.sshPort, 2222);
      expect(c.gatewayPort, 9000);
    });

    test('omits user and ssh port when absent', () {
      expect(buildSshGatewayUrl(host: 'h', gatewayPort: 8443),
          'ssh://h?port=8443');
    });
  });

  test('sshHostPort uses default 22 when ssh port absent', () {
    expect(sshHostPort(parseSshGatewayUrl('ssh://h')), 'h:22');
    expect(sshHostPort(parseSshGatewayUrl('ssh://h:2222')), 'h:2222');
  });
}
