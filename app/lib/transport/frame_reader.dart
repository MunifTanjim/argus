import 'dart:convert';
import 'dart:typed_data';

const maxFrameSize = 16 * 1024 * 1024;

const _cborMarker = 0x00;
const _cborHeaderSize = 5;

sealed class WireFrame {
  const WireFrame();
}

class JsonFrame extends WireFrame {
  final String line;
  const JsonFrame(this.line);
}

class CborFrame extends WireFrame {
  final Uint8List envelope;
  const CborFrame(this.envelope);
}

/// Prepends the CBOR frame header (marker byte + u32 big-endian length).
Uint8List frameCbor(Uint8List envelope) {
  final out = Uint8List(_cborHeaderSize + envelope.length);
  out[0] = _cborMarker;
  ByteData.sublistView(out).setUint32(1, envelope.length, Endian.big);
  out.setRange(_cborHeaderSize, out.length, envelope);
  return out;
}

/// Splits a byte stream of newline-delimited JSON lines and CBOR frames into
/// frames, regardless of how the bytes were chunked.
class FrameReader {
  Uint8List _buf = Uint8List(0);
  int _start = 0;
  int _end = 0;

  List<WireFrame> add(List<int> chunk) {
    _append(chunk);
    final out = <WireFrame>[];
    while (_start < _end) {
      if (_buf[_start] == _cborMarker) {
        if (_end - _start < _cborHeaderSize) break;
        final n = ByteData.sublistView(
          _buf,
          _start,
          _start + _cborHeaderSize,
        ).getUint32(1, Endian.big);
        if (n > maxFrameSize) {
          throw FormatException('cbor frame too large: $n bytes');
        }
        final total = _cborHeaderSize + n;
        if (_end - _start < total) break;
        out.add(
          CborFrame(_buf.sublist(_start + _cborHeaderSize, _start + total)),
        );
        _start += total;
        continue;
      }
      final nl = _buf.indexOf(0x0a, _start);
      final complete = nl >= 0 && nl < _end;
      if ((complete ? nl : _end) - _start > maxFrameSize) {
        throw const FormatException('json line too large');
      }
      if (!complete) break;
      final line = utf8.decoder.convert(_buf, _start, nl);
      _start = nl + 1;
      if (line.trim().isNotEmpty) out.add(JsonFrame(line));
    }
    if (_start == _end) {
      _start = _end = 0;
    }
    return out;
  }

  void _append(List<int> chunk) {
    if (_end + chunk.length > _buf.length) {
      final live = _end - _start;
      final need = live + chunk.length;
      if (need <= _buf.length) {
        _buf.setRange(0, live, _buf, _start);
      } else {
        var cap = _buf.isEmpty ? 256 : _buf.length;
        while (cap < need) {
          cap *= 2;
        }
        final next = Uint8List(cap)..setRange(0, live, _buf, _start);
        _buf = next;
      }
      _start = 0;
      _end = live;
    }
    _buf.setRange(_end, _end + chunk.length, chunk);
    _end += chunk.length;
  }
}
