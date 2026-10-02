import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

Uint8List _b(String key) => Uint8List.fromList(base64.decode(
    ((jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>)[key] as String));

void main() {
  test('loadChainInBackground matches an in-process load', () async {
    final entries = unmarshalChain(_b('enforcement_chain'));
    final bg = await loadChainInBackground(entries);
    final local = await TrustLog.load(entries);
    expect(bg.tip, local.tip);
    expect(bg.devices, local.devices);
    expect(bg.signers, local.signers);
  });

  test('loadChainInBackground rejects a tampered chain', () async {
    final entries = unmarshalChain(_b('enforcement_chain'));
    entries[1].sig![0] ^= 0xff;
    await expectLater(loadChainInBackground(entries), throwsA(isA<FormatException>()));
  });

  test('TrustStore loads chains through its loader', () async {
    var loads = 0;
    final store = TrustStore.tofu(loadChain: (entries) {
      loads++;
      return TrustLog.load(entries);
    });
    await store.ingest(_b('enforcement_chain'));
    expect(loads, 1);
    expect(store.locked, isTrue);
  });
}
