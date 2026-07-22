import 'dart:typed_data';

import '../symmetric_state.dart' show blake2s;
import 'entry.dart';

const int _maxField = 1 << 20;
const int _maxSigners = 1 << 12;
const int _maxDisablements = 1 << 12;
const int _maxEntries = 1 << 16;

/// The chain hash of a full (signed) entry: blake2s(sigBytes ‖ putField(sig)).
Uint8List hashEntry(Entry e) {
  final b = BytesBuilder();
  b.add(sigBytes(e));
  putField(b, e.sig);
  return blake2s(b.toBytes());
}

class _Reader {
  _Reader(this._b) : _view = ByteData.sublistView(_b);
  final Uint8List _b;
  final ByteData _view;
  int _off = 0;
  int get remaining => _b.length - _off;

  int readU8() {
    if (remaining < 1) throw const FormatException('trustlog: truncated');
    return _b[_off++];
  }

  int readU32() {
    if (remaining < 4) throw const FormatException('trustlog: truncated');
    final v = _view.getUint32(_off, Endian.big);
    _off += 4;
    return v;
  }

  int readCount(int cap) {
    final n = readU32();
    if (n > cap) throw const FormatException('trustlog: count exceeds cap');
    return n;
  }

  Uint8List? readField() {
    final n = readU32();
    if (n > _maxField) throw const FormatException('trustlog: field exceeds cap');
    if (n == 0) return null;
    if (remaining < n) throw const FormatException('trustlog: truncated field');
    final f = Uint8List.fromList(Uint8List.sublistView(_b, _off, _off + n));
    _off += n;
    return f;
  }
}

Entry _readEntry(_Reader r) {
  final kind = Kind.fromValue(r.readU8());
  if (kind == null) throw const FormatException('trustlog: unknown kind');
  final prev = r.readField();
  final sc = r.readCount(_maxSigners);
  final signers = [for (var i = 0; i < sc; i++) r.readField() ?? Uint8List(0)];
  final dc = r.readCount(_maxDisablements);
  final disablements = [for (var i = 0; i < dc; i++) r.readField() ?? Uint8List(0)];
  final key = r.readField();
  final signer = r.readField();
  final sig = r.readField();
  return Entry(
      kind: kind, prev: prev, signers: signers, disablements: disablements,
      key: key, signer: signer, sig: sig);
}

Entry unmarshalEntry(Uint8List b) {
  final r = _Reader(b);
  final e = _readEntry(r);
  if (r.remaining != 0) throw const FormatException('trustlog: trailing bytes after entry');
  return e;
}

List<Entry> unmarshalChain(Uint8List b) {
  final r = _Reader(b);
  final cnt = r.readCount(_maxEntries);
  final entries = <Entry>[];
  for (var i = 0; i < cnt; i++) {
    final raw = r.readField();
    if (raw == null) throw const FormatException('trustlog: empty entry');
    entries.add(unmarshalEntry(raw));
  }
  if (r.remaining != 0) throw const FormatException('trustlog: trailing bytes after chain');
  return entries;
}
