import 'package:flutter_test/flutter_test.dart';
import 'package:argus/transport/ssh_keygen.dart';
import 'package:argus/transport/ssh_key_store.dart';
import 'package:argus/transport/ssh_tunnel.dart';

void main() {
  test('openSshPublicKeyLine derives the generated key\'s public line', () {
    // 1024 bits keeps the test fast; we only exercise encoding, not strength.
    final g = generateRsaSshKey(bits: 1024, comment: 'argus@test');
    final derived = openSshPublicKeyLine(SshKey(g.privatePem));

    // Compare the type + base64 material (the first two fields) against the
    // generated line; the trailing comment differs (none vs argus@test).
    String material(String line) => line.split(' ').take(2).join(' ');
    expect(derived.startsWith('ssh-rsa '), isTrue);
    expect(material(derived), material(g.publicKeyLine));
  });

  test('openSshPublicKeyLine appends the comment as the final field', () {
    final g = generateRsaSshKey(bits: 1024);
    final derived = openSshPublicKeyLine(SshKey(g.privatePem), comment: 'laptop');
    expect(derived.endsWith(' laptop'), isTrue);
    // Comment is the third field; the material still parses cleanly.
    expect(derived.split(' '), hasLength(3));
  });

  test('openSshPublicKeyLine throws on a malformed key', () {
    expect(
      () => openSshPublicKeyLine(const SshKey('not a key')),
      throwsA(isA<SshTunnelException>()),
    );
  });
}
