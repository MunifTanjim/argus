import 'package:flutter_test/flutter_test.dart';
import 'package:argus/transport/ssh_keygen.dart';
import 'package:dartssh2/dartssh2.dart';

void main() {
  test('generates a parseable private key and an ssh-rsa public line', () {
    // 2048 bits keeps the test fast enough while exercising real generation.
    final g = generateRsaSshKey(bits: 2048, comment: 'argus@test');
    expect(g.publicKeyLine.startsWith('ssh-rsa '), isTrue);
    expect(g.publicKeyLine.endsWith(' argus@test'), isTrue);
    // dartssh2 must be able to load the generated private key.
    final pairs = SSHKeyPair.fromPem(g.privatePem);
    expect(pairs, isNotEmpty);
  });

  test('nextGeneratedKeyName is one past the highest existing number', () {
    expect(nextGeneratedKeyName([]), 'Generated key #1');
    expect(nextGeneratedKeyName(['Generated key #1']), 'Generated key #2');
    // Climbs past the highest even if lower numbers are missing.
    expect(nextGeneratedKeyName(['Generated key #2']), 'Generated key #3');
    expect(
      nextGeneratedKeyName(['other', 'Generated key #2', 'Generated key #5']),
      'Generated key #6',
    );
  });
}
