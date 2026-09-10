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

void main() {
  group('supersedingGenesis', () {
    late Map<String, dynamic> v;
    setUp(() => v = _tl());

    test('prefers a live root over a dead one', () {
      final dead = _b(v, 'disabled_chain');
      final live = _b(v, 'wrong_genesis_chain');
      final got = supersedingGenesis([dead, live], null);
      expect(got, equals(_genesisOf(live)));
    });

    test('prefers the longest live root', () {
      final a = _b(v, 'chain');
      final b = _b(v, 'wrong_genesis_chain');
      final longer = chainEntries(a).length >= chainEntries(b).length ? a : b;
      final got = supersedingGenesis([a, b], null);
      expect(got, equals(_genesisOf(longer)));
    });

    test('skips our own root', () {
      final own = _b(v, 'chain');
      final other = _b(v, 'wrong_genesis_chain');
      final got = supersedingGenesis([own, other], _genesisOf(own));
      expect(got, equals(_genesisOf(other)));
    });

    test('falls back to a dead root when every offered root is dead', () {
      final dead = _b(v, 'disabled_chain');
      final got = supersedingGenesis([dead], null);
      expect(got, equals(_genesisOf(dead)));
    });

    test('yields nothing for garbage', () {
      final garbage = Uint8List.fromList(utf8.encode('not a chain'));
      expect(supersedingGenesis([garbage], null), isNull);
    });
  });

  group('detectSupersession', () {
    late Map<String, dynamic> v;
    setUp(() => v = _tl());

    test('names a live successor and carries its chain to adopt', () {
      final dead = _b(v, 'disabled_chain');
      final live = _b(v, 'wrong_genesis_chain');
      final own = _genesisOf(_b(v, 'chain'));

      final got = detectSupersession([dead, live], own);

      expect(got, isNotNull);
      expect(got!.genesis, equals(_genesisOf(live)));
      expect(got.liveChain, equals(live));
    });

    test('names a dead fallback but carries no chain to adopt', () {
      final dead = _b(v, 'disabled_chain');
      // own must be a genuinely different root; disabled_chain is a disabled fork
      // of `chain`, so it shares that genesis.
      final own = _genesisOf(_b(v, 'wrong_genesis_chain'));

      final got = detectSupersession([dead], own);

      expect(got, isNotNull);
      expect(got!.genesis, equals(_genesisOf(dead)));
      expect(got.liveChain, isNull);
    });

    test('returns nothing when only our own root is offered', () {
      final own = _b(v, 'chain');
      expect(detectSupersession([own], _genesisOf(own)), isNull);
    });

    test('returns nothing for garbage', () {
      final garbage = Uint8List.fromList(utf8.encode('not a chain'));
      expect(detectSupersession([garbage], _genesisOf(_b(v, 'chain'))), isNull);
    });
  });
}
