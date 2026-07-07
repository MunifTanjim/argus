import 'dart:convert';

/// Decodes a UTF-8 byte stream to text across chunk boundaries.
///
/// The node reads the PTY at arbitrary byte offsets (up to a fixed chunk size)
/// and base64-encodes each read independently, so a multi-byte codepoint — the
/// box-drawing and other glyphs the agent TUIs use heavily — can straddle two
/// `terminal.output` chunks. Decoding each chunk in isolation with
/// `allowMalformed` would replace the split halves with U+FFFD on both sides.
///
/// This holds back an incomplete trailing sequence until its continuation bytes
/// arrive, while still replacing genuinely invalid bytes rather than throwing.
class Utf8StreamDecoder {
  Utf8StreamDecoder() {
    _sink = const Utf8Decoder(allowMalformed: true)
        .startChunkedConversion(_out);
  }

  final _CallbackStringSink _out = _CallbackStringSink();
  late final ByteConversionSink _sink;

  /// Feeds [bytes] and returns the text decodable so far. Bytes belonging to an
  /// incomplete trailing multi-byte sequence are buffered until the next [add].
  String add(List<int> bytes) {
    _sink.add(bytes);
    return _out.take();
  }
}

/// A `Sink<String>` that accumulates decoded chunks for retrieval via [take].
class _CallbackStringSink implements Sink<String> {
  final StringBuffer _buf = StringBuffer();

  @override
  void add(String chunk) => _buf.write(chunk);

  @override
  void close() {}

  String take() {
    final s = _buf.toString();
    _buf.clear();
    return s;
  }
}
