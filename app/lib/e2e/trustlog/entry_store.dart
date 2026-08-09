import 'dart:typed_data';

import 'package:flutter/foundation.dart' show visibleForTesting;

import '../bytes.dart' show hexEncode;
import 'codec.dart' show hashEntry, unmarshalEntry;
import 'entry.dart' show Entry;

// DoS backstop: a locked network performs a handful of trust-log writes a year,
// so reaching this ceiling means a peer is misbehaving. Nothing is evicted —
// inserts past the ceiling are refused.
int _maxRetainedEntries = 1 << 16;

/// Overrides the entry store ceiling and returns the previous value. Pass the
/// returned value to [addTearDown] to restore it after the test. Test-only —
/// matches Go's [SetMaxRetainedEntriesForTest].
@visibleForTesting
int setMaxRetainedEntriesForTest(int n) {
  final prev = _maxRetainedEntries;
  _maxRetainedEntries = n;
  return prev;
}

// Two orders of magnitude above any realistic log and well below
// _maxRetainedEntries. Truncating is safe: the protocol is set subtraction,
// so an under-reporting caller receives entries it already holds, which dedupe
// by hash on arrival.
int _maxOfferedHashes = 4096;

/// Overrides the offer cap and returns the previous value. Pass the returned
/// value to [addTearDown] to restore it after the test. Test-only — matches
/// Go's [SetMaxOfferedHashesForTest].
@visibleForTesting
int setMaxOfferedHashesForTest(int n) {
  final prev = _maxOfferedHashes;
  _maxOfferedHashes = n;
  return prev;
}

/// Holds raw trust-log entries keyed by entry hash. Intentionally blind: parses
/// only each entry's own hash and its Prev pointer — never a signature, a kind,
/// or any payload.
///
/// Lifetime coupling: the retained entry set and the advertised offer share the
/// same in-memory lifetime. After a restart both are empty, so the client
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

  /// Lists the hash of every retained entry, for a sync offer. [truncated] is
  /// true when the store holds more than [_maxOfferedHashes] entries and the
  /// list is partial. Truncating is safe: the protocol is set subtraction, so
  /// an under-reporting caller receives entries it already holds.
  (List<Uint8List> hashes, bool truncated) hashes() {
    final result = <Uint8List>[];
    for (final h in _byHash.keys) {
      if (result.length >= _maxOfferedHashes) return (result, true);
      result.add(_hexToBytes(h));
    }
    return (result, false);
  }

  /// Returns every retained entry, parents before children.
  List<Uint8List> all() {
    final (entries, _, _) = delta([]);
    return entries;
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

  /// Returns every retained entry the caller did not list in [known], ordered
  /// parents-before-children. Also returns in [want] the listed hashes this
  /// store does not hold. [disjoint] is true when [known] is non-empty and
  /// shares no entry with this store. This is set subtraction: the gateway
  /// does not assume the caller holds a listed entry's ancestry.
  (List<Uint8List> entries, List<Uint8List> want, bool disjoint) delta(
      List<Uint8List> known) {
    final held = <String, bool>{};
    final want = <Uint8List>[];
    var shared = 0;

    for (final h in known) {
      final key = hexEncode(h);
      if (_byHash.containsKey(key)) {
        held[key] = true;
        shared++;
      } else {
        want.add(Uint8List.fromList(h));
      }
    }

    final emitted = <String>{};
    final result = <Uint8List>[];

    void emit(String h) {
      if (h.isEmpty || emitted.contains(h)) return;
      if (!_byHash.containsKey(h)) return;
      // Mark visited before recursing so a prev cycle terminates here rather
      // than overflowing the stack. Parents-before-children still holds: the
      // append is after emit(prev) returns, so ancestors are always emitted first.
      emitted.add(h);
      // Always recurse into prev so ancestors of a held hash are not withheld;
      // skip only the append when held, not the descent.
      emit(_prev[h] ?? '');
      if (!held.containsKey(h)) {
        result.add(Uint8List.fromList(_byHash[h]!));
      }
    }

    for (final h in heads()) {
      emit(hexEncode(h));
    }
    return (result, want, known.isNotEmpty && shared == 0);
  }

  /// Bypasses [put] to inject a raw (hash, bytes, prevHash) triple directly
  /// into the internal maps. Test-only — used to construct prev cycles that a
  /// well-formed hash-linked log cannot produce. Never call in production code.
  @visibleForTesting
  void injectRawForTest(String hash, Uint8List raw, String prevHash) {
    _byHash[hash] = raw;
    _prev[hash] = prevHash;
    _count++;
  }

  Uint8List _hexToBytes(String h) {
    final bytes = Uint8List(h.length ~/ 2);
    for (var i = 0; i < bytes.length; i++) {
      bytes[i] = int.parse(h.substring(i * 2, i * 2 + 2), radix: 16);
    }
    return bytes;
  }
}
