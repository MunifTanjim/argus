import 'dart:convert';
import 'dart:typed_data';

import 'package:cbor/simple.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/transport/frame_reader.dart';

void main() {
  test('reassembles mixed and split frames', () {
    final env = Uint8List.fromList(cbor.encode({'b': 2}));
    final bytes = <int>[
      ...utf8.encode('{"a":1}\n'),
      ...frameCbor(env),
      ...utf8.encode('\n{"c":3}\n'),
    ];
    final r = FrameReader();
    final out = <WireFrame>[];
    for (final b in bytes) {
      out.addAll(r.add([b]));
    }
    expect(out, hasLength(3));
    expect((out[0] as JsonFrame).line, '{"a":1}');
    expect((out[1] as CborFrame).envelope, env);
    expect((out[2] as JsonFrame).line, '{"c":3}');
  });

  test('reads several frames from one chunk', () {
    final env = Uint8List.fromList(cbor.encode({'b': 2}));
    final out = FrameReader().add([
      ...frameCbor(env),
      ...utf8.encode('{"c":3}\n'),
    ]);
    expect(out, hasLength(2));
    expect(out[0], isA<CborFrame>());
    expect((out[1] as JsonFrame).line, '{"c":3}');
  });

  test('rejects an oversized cbor frame', () {
    expect(
      () => FrameReader().add([0x00, 0x01, 0x00, 0x00, 0x01]),
      throwsFormatException,
    );
  });

  test('rejects an oversized json line', () {
    final r = FrameReader();
    expect(
      () => r.add(
        Uint8List(maxFrameSize + 1)..fillRange(0, maxFrameSize + 1, 0x61),
      ),
      throwsFormatException,
    );
  });

  test('frameCbor prepends marker and big-endian length', () {
    final f = frameCbor(Uint8List.fromList([1, 2, 3]));
    expect(f, [0x00, 0, 0, 0, 3, 1, 2, 3]);
  });
}
