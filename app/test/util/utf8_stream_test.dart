import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/util/utf8_stream.dart';

void main() {
  test('passes through a chunk that ends on a codepoint boundary', () {
    final d = Utf8StreamDecoder();
    expect(d.add(utf8.encode('hello')), 'hello');
  });

  test('reassembles a multi-byte glyph split across two chunks', () {
    final d = Utf8StreamDecoder();
    final bytes = utf8.encode('€'); // U+20AC → [0xE2, 0x82, 0xAC]
    expect(bytes.length, 3);
    // First chunk carries an incomplete sequence: nothing decodable yet, and it
    // must NOT emit a replacement char for the partial bytes.
    final first = d.add(bytes.sublist(0, 2));
    expect(first, isEmpty);
    // Continuation completes the glyph.
    final second = d.add(bytes.sublist(2));
    expect(first + second, '€');
  });

  test('reassembles across many one-byte-at-a-time chunks', () {
    final d = Utf8StreamDecoder();
    final bytes = utf8.encode('a→b'); // '→' is 3 bytes
    final out = StringBuffer();
    for (final b in bytes) {
      out.write(d.add([b]));
    }
    expect(out.toString(), 'a→b');
  });

  test('keeps preceding complete text when a chunk ends mid-sequence', () {
    final d = Utf8StreamDecoder();
    final glyph = utf8.encode('☃'); // 3 bytes
    // "hi" + first 2 bytes of the snowman.
    final chunk = <int>[...utf8.encode('hi'), glyph[0], glyph[1]];
    expect(d.add(chunk), 'hi');
    expect(d.add([glyph[2]]), '☃');
  });

  test('still replaces genuinely invalid bytes rather than throwing', () {
    final d = Utf8StreamDecoder();
    // 0xFF is never valid in UTF-8; must not throw, must emit a replacement.
    final out = d.add([0xFF]) + d.add(utf8.encode('x'));
    expect(out.contains('x'), isTrue);
    expect(out.codeUnits.contains(0xFFFD), isTrue);
  });
}
