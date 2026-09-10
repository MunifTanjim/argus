import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

Map<String, dynamic> _tl() =>
    (jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>;

Uint8List _b(Map<String, dynamic> v, String k) =>
    Uint8List.fromList(base64.decode(v[k] as String));

Uint8List _genesisOf(Uint8List chain) =>
    hashEntry(unmarshalEntry(chainEntries(chain).first));

Uint8List _tipOf(Uint8List chain) =>
    hashEntry(unmarshalEntry(chainEntries(chain).last));

void main() {
  group('TrustStore.reanchor', () {
    late Map<String, dynamic> v;
    setUp(() => v = _tl());

    test('adopts a valid chain under a new genesis, replacing a disabled anchor',
        () async {
      final dead = _b(v, 'disabled_chain');
      final live = _b(v, 'wrong_genesis_chain');
      final store = TrustStore(_genesisOf(dead));
      await store.ingest(dead);
      expect(store.disabled, isTrue, reason: 'precondition: pinned to a dead root');

      final adopted = await store.reanchor(live);

      expect(adopted, isTrue);
      expect(store.disabled, isFalse);
      expect(store.tip, equals(_tipOf(live)));
      expect(store.genesisHash, equals(_genesisOf(live)));
    });

    test('keeps the current anchor when the candidate does not verify', () async {
      final dead = _b(v, 'disabled_chain');
      final live = _b(v, 'wrong_genesis_chain');
      final store = TrustStore(_genesisOf(dead));
      await store.ingest(dead);

      final bad = Uint8List.fromList(live);
      bad[bad.length - 1] ^= 0xFF; // corrupt the last entry's signature

      await expectLater(store.reanchor(bad), throwsA(isA<Exception>()));
      expect(store.disabled, isTrue, reason: 'old anchor survived a bad re-pin');
      expect(store.genesisHash, equals(_genesisOf(dead)));
    });
  });
}
