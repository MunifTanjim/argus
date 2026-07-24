import 'dart:convert';
import 'dart:typed_data';

import 'package:cryptography_plus/cryptography_plus.dart';
import 'package:cryptography_plus/dart.dart';

// Fixed Argon2id parameters — must match Go trustlog.DisablementCommitment:
// memory 64 MiB (65536 KiB blocks), 1 iteration, 4 lanes, 32-byte output.
final DartArgon2id _argon2id =
    const DartArgon2id(memory: 65536, iterations: 1, parallelism: 4, hashLength: 32);
final List<int> _salt = utf8.encode('argus-trustlog-disablement-v1');

/// The one-way Argon2id commitment for a disablement secret (deterministic;
/// recomputed to verify a revealed KindDisable secret against the genesis).
Future<Uint8List> disablementCommitment(List<int> secret) async {
  final derived = await _argon2id.deriveKey(
    secretKey: SecretKeyData(secret),
    nonce: _salt,
  );
  return Uint8List.fromList(await derived.extractBytes());
}
