import 'package:flutter_test/flutter_test.dart';
import 'package:argus/transport/ssh_public_key.dart';

void main() {
  // Known vector: e = 65537 (0x010001), n = 0x00C0FFEE (leading zero byte in n
  // is dropped, but its high bit is clear so no 0x00 pad is added; 0xC0 has the
  // high bit set so a pad IS added). We assert the exact base64 encoding.
  test('serializes ssh-rsa with correct mpint padding', () {
    final line = rsaOpenSshPublicKey(BigInt.parse('C0FFEE', radix: 16),
        BigInt.from(65537),
        comment: 'argus@device');
    // Wire = len(7)"ssh-rsa" | len(3)\x01\x00\x01 | len(4)\x00\xC0\xFF\xEE
    // base64 of that byte sequence:
    expect(line,
        'ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAABADA/+4= argus@device');
  });

  test('omits trailing space when comment is empty', () {
    final line = rsaOpenSshPublicKey(BigInt.from(3), BigInt.from(65537));
    expect(line.startsWith('ssh-rsa '), isTrue);
    expect(line.endsWith(' '), isFalse);
  });
}
