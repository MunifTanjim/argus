import 'dart:convert';
import 'dart:io';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/e2e/trustlog/entry_store.dart' show setMaxRetainedEntriesForTest, setMaxOfferedHashesForTest;

Map<String, dynamic> _tl() =>
    (jsonDecode(File('test/e2e/testdata/vectors.json').readAsStringSync())
        as Map<String, dynamic>)['trustlog'] as Map<String, dynamic>;

Uint8List _b(Map<String, dynamic> v, String k) =>
    Uint8List.fromList(base64.decode(v[k] as String));

List<Uint8List> _rawEntries(Uint8List chain) => chainEntries(chain);

void main() {
  group('EntryStore', () {
    test('dedupes by hash', () {
      final v = _tl();
      final entries = _rawEntries(_b(v, 'chain'));
      final store = EntryStore();
      final (a1, _) = store.putAll(entries);
      expect(a1, entries.length, reason: 'first insert: all new');
      final (a2, r2) = store.putAll(entries);
      expect(a2, 0, reason: 're-offer same entries: none inserted');
      expect(r2, 0, reason: 'dedup must not set refused');
    });

    test('an append does not create a second head', () {
      final v = _tl();
      final all = _rawEntries(_b(v, 'chain'));
      expect(all.length, greaterThanOrEqualTo(2));
      final store = EntryStore();
      store.put(all[0]); // genesis alone
      expect(store.heads().length, 1, reason: 'genesis alone: 1 head');
      store.putAll(all.sublist(1)); // append remaining entries
      expect(store.heads().length, 1,
          reason: 'after appending, old head becomes ancestor; still 1 head');
    });

    test('two branches sharing a genesis give two heads', () {
      final v = _tl();
      final store = EntryStore();
      store.putAll(_rawEntries(_b(v, 'chain')));
      store.putAll(_rawEntries(_b(v, 'fork_chain')));
      expect(store.heads().length, 2,
          reason: 'two competing forks must produce exactly 2 heads');
    });

    test('ceiling refuses rather than evicts — dedup and refused are distinct', () {
      // The Dart ceiling (1<<16) cannot be driven to in a unit test without
      // generating 65536 valid signed entries. We verify the structural contract:
      // re-offering an already-stored entry never sets refused (it is dedup,
      // not a refusal), which proves refused tracks only the ceiling condition.
      final v = _tl();
      final store = EntryStore();
      store.putAll(_rawEntries(_b(v, 'chain')));
      final (_, refused) = store.putAll(_rawEntries(_b(v, 'chain')));
      expect(refused, 0, reason: 'dedup must not report refused');
    });

    test('garbage entries are silently skipped and do not set refused', () {
      final store = EntryStore();
      final (stored, refused) = store.put(Uint8List.fromList(utf8.encode('garbage')));
      expect(stored, isFalse);
      expect(refused, isFalse);
    });

    test('delta with empty known-heads returns all retained entries', () {
      final v = _tl();
      final entries = _rawEntries(_b(v, 'chain'));
      final store = EntryStore();
      store.putAll(entries);
      final (all, want) = store.delta([]);
      expect(all.length, entries.length,
          reason: 'no known heads: receive everything');
      expect(want, isEmpty);
    });

    test('delta withholds what the caller can reach from its known heads', () {
      final v = _tl();
      final store = EntryStore();
      store.putAll(_rawEntries(_b(v, 'chain')));
      final (diff, want) = store.delta(store.heads());
      expect(diff, isEmpty,
          reason: 'caller holds the head: nothing to receive');
      expect(want, isEmpty);
    });

    test('delta reports unknown caller heads in want', () {
      final v = _tl();
      final store = EntryStore();
      store.putAll(_rawEntries(_b(v, 'chain')));
      final unknownHead = Uint8List(32)..fillRange(0, 32, 0xAB);
      final (_, want) = store.delta([unknownHead]);
      expect(want.length, 1, reason: 'unknown head must appear in want');
      expect(want[0], equals(unknownHead));
    });

    test('delta orders parents before children', () {
      final v = _tl();
      final store = EntryStore();
      store.putAll(_rawEntries(_b(v, 'chain')));
      final (all, _) = store.delta([]);
      final seenHashes = <String>{};
      for (final raw in all) {
        final e = unmarshalEntry(raw);
        final prev = e.prev;
        if (prev != null && prev.isNotEmpty) {
          expect(seenHashes, contains(hexEncode(prev)),
              reason: 'child emitted before its parent');
        }
        seenHashes.add(hexEncode(hashEntry(e)));
      }
    });

    group('ceiling', () {
      test('refuses past the ceiling rather than evicting', () {
        final v = _tl();
        final entries = _rawEntries(_b(v, 'chain'));
        expect(entries.length, greaterThanOrEqualTo(2));
        final prevCeiling = setMaxRetainedEntriesForTest(1);
        addTearDown(() => setMaxRetainedEntriesForTest(prevCeiling));

        final store = EntryStore();
        final (stored, refused) = store.put(entries[0]);
        expect(stored, isTrue, reason: 'first insert must succeed');
        expect(refused, isFalse);

        final (stored2, refused2) = store.put(entries[1]);
        expect(stored2, isFalse, reason: 'insert past ceiling must not be stored');
        expect(refused2, isTrue, reason: 'insert past ceiling must set refused');
      });

      test('putAll refused count is non-zero at ceiling', () {
        final v = _tl();
        final entries = _rawEntries(_b(v, 'chain'));
        expect(entries.length, greaterThanOrEqualTo(2));
        final prevCeiling = setMaxRetainedEntriesForTest(1);
        addTearDown(() => setMaxRetainedEntriesForTest(prevCeiling));

        final store = EntryStore();
        store.put(entries[0]); // fills to ceiling
        final (added, refused) = store.putAll(entries.sublist(1));
        expect(added, 0, reason: 'at ceiling: nothing new added');
        expect(refused, entries.length - 1,
            reason: 'at ceiling: every remaining entry must be refused');
      });

      test('invariant: at ceiling a branch must NOT add its head to the head set', () {
        // This is the core invariant the entry store guards:
        // a head is recorded ONLY if its raw bytes were retained.
        // If put refuses (ceiling), the entry is absent from byHash, so
        // heads() cannot return it. The head set is derived strictly from
        // what is stored — a refused entry is invisible to heads().
        final v = _tl();
        final entries = _rawEntries(_b(v, 'chain'));
        expect(entries.length, greaterThanOrEqualTo(2));
        final prevCeiling = setMaxRetainedEntriesForTest(1);
        addTearDown(() => setMaxRetainedEntriesForTest(prevCeiling));

        final store = EntryStore();
        store.put(entries[0]); // fills ceiling with genesis
        final headsBefore = store.heads();
        expect(headsBefore.length, 1);

        // The auth entry (prev = genesis hash) is refused; its head must
        // NOT appear in heads() even though it would be a tip.
        final (stored, refused) = store.put(entries[1]);
        expect(stored, isFalse);
        expect(refused, isTrue);

        // Head set is unchanged — the refused entry is not recorded.
        expect(store.heads().length, 1,
            reason: 'refused entry must not appear as a new head');
      });
    });

    test('retains an orphan entry whose ancestor is absent', () {
      final v = _tl();
      final entries = _rawEntries(_b(v, 'chain'));
      expect(entries.length, greaterThanOrEqualTo(2));
      final store = EntryStore();
      store.put(entries[1]); // non-genesis without its parent
      final (all, _) = store.delta([]);
      expect(all.length, 1, reason: 'orphan must be retained and served');
    });

    group('hashes', () {
      test('hashes lists every retained entry', () {
        final v = _tl();
        final raw = _rawEntries(_b(v, 'chain'));
        final store = EntryStore();
        store.putAll(raw);
        final (hashes, truncated) = store.hashes();
        expect(truncated, isFalse);
        expect(hashes.length, raw.length);
      });

      test('hashes truncates at the cap', () {
        final prev = setMaxOfferedHashesForTest(1);
        addTearDown(() => setMaxOfferedHashesForTest(prev));
        final v = _tl();
        final store = EntryStore();
        store.putAll(_rawEntries(_b(v, 'chain')));
        final (hashes, truncated) = store.hashes();
        expect(truncated, isTrue);
        expect(hashes.length, 1);
      });
    });

    test('all returns every retained entry parents-first', () {
      final v = _tl();
      final store = EntryStore();
      store.putAll(_rawEntries(_b(v, 'chain')));
      store.putAll(_rawEntries(_b(v, 'fork_chain')));
      final all = store.all();
      expect(all.length, greaterThan(0));
      final seenHashes = <String>{};
      for (final raw in all) {
        final e = unmarshalEntry(raw);
        final prev = e.prev;
        if (prev != null && prev.isNotEmpty) {
          expect(seenHashes, contains(hexEncode(prev)),
              reason: 'child emitted before its parent');
        }
        seenHashes.add(hexEncode(hashEntry(e)));
      }
    });
  });
}
