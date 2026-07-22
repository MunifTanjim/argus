import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';

Map<String, dynamic> _tl() =>
    (jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>;

void main() {
  test('unmarshalChain decodes the Go chain; hashEntry(genesis) == pinned head', () {
    final v = _tl();
    final entries = unmarshalChain(Uint8List.fromList(base64.decode(v['chain'] as String)));
    expect(entries.length, 2); // genesis + authorizeDevice
    expect(entries.first.kind, Kind.genesis);
    expect(hashEntry(entries.first),
        equals(Uint8List.fromList(base64.decode(v['genesis_head'] as String))));
    // Folding the last entry's hash reproduces the Go head.
    expect(hashEntry(entries.last), equals(Uint8List.fromList(base64.decode(v['head'] as String))));
  });

  test('unmarshalChain rejects truncation and trailing bytes', () {
    final v = _tl();
    final chain = base64.decode(v['chain'] as String);
    expect(() => unmarshalChain(Uint8List.fromList(chain.sublist(0, chain.length - 3))),
        throwsA(isA<FormatException>()));
    expect(() => unmarshalChain(Uint8List.fromList([...chain, 0])),
        throwsA(isA<FormatException>()));
  });
}
