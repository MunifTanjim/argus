import 'dart:typed_data';

import '../bytes.dart' show hexEncode;
import 'codec.dart' show hashEntry, unmarshalEntry;
import 'entry.dart' show Entry;

// DoS backstop: a locked network performs a handful of trust-log writes a year,
// so reaching this ceiling means a peer is misbehaving. Nothing is evicted —
// inserts past the ceiling are refused.
const int _maxRetainedEntries = 1 << 16;

/// Holds raw trust-log entries keyed by entry hash. Intentionally blind: parses
/// only each entry's own hash and its Prev pointer — never a signature, a kind,
/// or any payload.
///
/// Lifetime coupling: the retained entry set and the advertised head set share
/// the same in-memory lifetime. After a restart both are empty, so the client
/// advertises nothing and receives everything from the gateway — no
/// partial-sync gap can arise.
class EntryStore {
  final _byHash = <String, Uint8List>{}; // hex hash -> raw bytes
  final _prev = <String, String>{}; // hex hash -> prev hex hash ('' for genesis)
  int _count = 0;

  /// Stores a single raw entry. [stored] is true when newly added; [refused] is
  /// true specifically when the store is at the ceiling. Duplicates and
  /// undecodable entries return (false, false).
  (bool stored, bool refused) put(Uint8List raw) {
    Entry e;
    try {
      e = unmarshalEntry(raw);
    } catch (_) {
      return (false, false);
    }
    final h = hexEncode(hashEntry(e));
    if (_byHash.containsKey(h)) return (false, false);
    if (_count >= _maxRetainedEntries) return (false, true);
    _byHash[h] = Uint8List.fromList(raw);
    _prev[h] = e.prev != null && e.prev!.isNotEmpty ? hexEncode(e.prev!) : '';
    _count++;
    return (true, false);
  }

  /// Stores each raw entry. Returns (added, refused) counts. Duplicates and
  /// undecodable garbage contribute to neither count.
  (int added, int refused) putAll(List<Uint8List> entries) {
    var added = 0;
    var refused = 0;
    for (final raw in entries) {
      final (s, r) = put(raw);
      if (s) added++;
      if (r) refused++;
    }
    return (added, refused);
  }

  /// Returns the hash of every retained entry that no other retained entry
  /// names as its Prev — the tips of all branches.
  List<Uint8List> heads() {
    final referenced = <String>{};
    for (final p in _prev.values) {
      if (p.isNotEmpty) referenced.add(p);
    }
    return [
      for (final h in _byHash.keys)
        if (!referenced.contains(h)) _hexToBytes(h),
    ];
  }

  /// Returns every retained entry the caller cannot reach by walking Prev from
  /// [knownHeads], ordered parents-before-children. Also returns caller heads
  /// this store does not hold as the second element.
  (List<Uint8List> entries, List<Uint8List> want) delta(List<Uint8List> knownHeads) {
    final reachable = <String>{};
    final want = <Uint8List>[];

    for (final h in knownHeads) {
      final key = hexEncode(h);
      if (!_byHash.containsKey(key)) {
        want.add(Uint8List.fromList(h));
        continue;
      }
      var cur = key;
      while (cur.isNotEmpty) {
        if (reachable.contains(cur)) break;
        reachable.add(cur);
        cur = _prev[cur] ?? '';
      }
    }

    final emitted = <String>{};
    final result = <Uint8List>[];

    void emit(String h) {
      if (h.isEmpty || emitted.contains(h) || reachable.contains(h)) return;
      if (!_byHash.containsKey(h)) return;
      emit(_prev[h] ?? '');
      emitted.add(h);
      result.add(Uint8List.fromList(_byHash[h]!));
    }

    for (final h in heads()) {
      emit(hexEncode(h));
    }
    return (result, want);
  }

  Uint8List _hexToBytes(String h) {
    final bytes = Uint8List(h.length ~/ 2);
    for (var i = 0; i < bytes.length; i++) {
      bytes[i] = int.parse(h.substring(i * 2, i * 2 + 2), radix: 16);
    }
    return bytes;
  }
}
