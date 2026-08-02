import 'dart:typed_data';

import '../bytes.dart' show hexEncode, putUint32;
import 'codec.dart' show hashEntry, unmarshalEntry;
import 'entry.dart' show Entry;

/// Splits a marshalled chain into its individually marshalled entries without
/// re-encoding. Mirrors Go's ChainEntries: the chain format is a 4-byte
/// big-endian count followed by each entry length-prefixed with putField.
List<Uint8List> chainEntries(Uint8List chain) {
  final view = ByteData.sublistView(chain);
  var off = 0;
  if (chain.length < 4) throw const FormatException('trustlog: truncated chain');
  final cnt = view.getUint32(off, Endian.big);
  off += 4;
  final result = <Uint8List>[];
  for (var i = 0; i < cnt; i++) {
    if (off + 4 > chain.length) throw const FormatException('trustlog: truncated entry length');
    final len = view.getUint32(off, Endian.big);
    off += 4;
    if (off + len > chain.length) throw const FormatException('trustlog: truncated entry data');
    result.add(Uint8List.sublistView(chain, off, off + len));
    off += len;
  }
  if (off != chain.length) throw const FormatException('trustlog: trailing bytes after chain');
  return result;
}

/// Groups individually marshalled entries into complete genesis-rooted chains
/// encoded identically to Go's MarshalChain: a 4-byte big-endian count followed
/// by each raw entry length-prefixed with putField. Entries that do not decode
/// are ignored; a branch whose head cannot walk Prev back to a nil-Prev genesis
/// is skipped — a chain with a hole cannot be verified. Duplicates collapse.
///
/// The raw entry bytes are used as-is (never re-encoded through marshalEntry),
/// so the chain bytes are byte-identical to what the gateway would have sent.
List<Uint8List> assembleChains(List<Uint8List> rawEntries) {
  final byHash = <String, (Entry, Uint8List)>{};
  for (final raw in rawEntries) {
    Entry e;
    try {
      e = unmarshalEntry(raw);
    } catch (_) {
      continue;
    }
    final h = hexEncode(hashEntry(e));
    byHash.putIfAbsent(h, () => (e, Uint8List.fromList(raw)));
  }

  final referenced = <String>{};
  for (final (e, _) in byHash.values) {
    final prev = e.prev;
    if (prev != null && prev.isNotEmpty) {
      referenced.add(hexEncode(prev));
    }
  }

  final chains = <Uint8List>[];

  for (final head in byHash.keys) {
    if (referenced.contains(head)) continue;

    // Walk back to genesis; stop after byHash.length steps (cycle guard).
    final reversed = <String>[];
    var cur = head;
    var ok = false;
    final maxSteps = byHash.length;
    for (var i = 0; i < maxSteps; i++) {
      final pair = byHash[cur];
      if (pair == null) break; // missing ancestor
      reversed.add(cur);
      final prev = pair.$1.prev;
      if (prev == null || prev.isEmpty) {
        ok = true;
        break; // reached genesis
      }
      cur = hexEncode(prev);
    }
    if (!ok) continue;

    final buf = BytesBuilder();
    putUint32(buf, reversed.length);
    for (final h in reversed.reversed) {
      final raw = byHash[h]!.$2;
      putUint32(buf, raw.length);
      buf.add(raw);
    }
    chains.add(buf.toBytes());
  }
  return chains;
}
