import 'package:basic_utils/basic_utils.dart';
import 'package:flutter/foundation.dart';

import 'ssh_public_key.dart';

class GeneratedSshKey {
  const GeneratedSshKey(this.privatePem, this.publicKeyLine);
  final String privatePem; // PEM dartssh2 can load
  final String publicKeyLine; // authorized_keys line
}

/// Generate an RSA keypair: private key as PEM (loadable by dartssh2), public
/// key as an OpenSSH authorized_keys line.
GeneratedSshKey generateRsaSshKey({
  int bits = 3072,
  String comment = 'argus@device',
}) {
  final pair = CryptoUtils.generateRSAKeyPair(keySize: bits);
  final priv = pair.privateKey as RSAPrivateKey;
  final pub = pair.publicKey as RSAPublicKey;

  final pem = CryptoUtils.encodeRSAPrivateKeyToPemPkcs1(priv);
  final line = rsaOpenSshPublicKey(pub.modulus!, pub.exponent!,
      comment: comment);
  return GeneratedSshKey(pem, line);
}

/// Runs [generateRsaSshKey] on a background isolate so RSA generation does not
/// block the UI.
Future<GeneratedSshKey> generateRsaSshKeyAsync({
  int bits = 3072,
  String comment = 'argus@device',
}) =>
    compute(_generateRsaSshKeyForArgs, (bits, comment));

// compute() requires a top-level (non-closure) callback taking a single arg.
GeneratedSshKey _generateRsaSshKeyForArgs((int, String) args) =>
    generateRsaSshKey(bits: args.$1, comment: args.$2);

/// The default name for the next generated key: `Generated key #N`, where N is
/// one past the highest existing `Generated key #<n>` in [existing]. Always
/// suffixed (first is #1), and keeps climbing even if lower numbers were deleted.
String nextGeneratedKeyName(Iterable<String> existing) {
  final re = RegExp(r'^Generated key #(\d+)$');
  var max = 0;
  for (final name in existing) {
    final m = re.firstMatch(name);
    if (m != null) {
      final n = int.parse(m.group(1)!);
      if (n > max) max = n;
    }
  }
  return 'Generated key #${max + 1}';
}
