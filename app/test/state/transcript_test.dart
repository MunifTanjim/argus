import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/chunk.dart';
import 'package:argus/state/transcript.dart';
import 'package:argus/state/transcript_controller.dart';

final _provider =
    NotifierProvider<TranscriptNotifier, TranscriptState>(TranscriptNotifier.new);

Chunk _c(String id) => Chunk(id: id, kind: ChunkKind.user, text: id);
TranscriptDelta _d(String sub, int from, List<String> ids) =>
    TranscriptDelta(subId: sub, fromIndex: from, chunks: ids.map(_c).toList());

void main() {
  test('applyDelta appends from index, clamps overflow', () {
    var chunks = applyDelta(const [], _d('s', 0, ['a', 'b']));
    expect(chunks.map((c) => c.id), ['a', 'b']);

    chunks = applyDelta(chunks, _d('s', 1, ['B', 'c'])); // replace from idx 1
    expect(chunks.map((c) => c.id), ['a', 'B', 'c']);

    chunks = applyDelta(chunks, _d('s', 99, ['d'])); // overflow clamps to end
    expect(chunks.map((c) => c.id), ['a', 'B', 'c', 'd']);
  });

  test('newSubId is 16 hex chars and unique', () {
    final a = newSubId();
    final b = newSubId();
    expect(a, matches(RegExp(r'^[0-9a-f]{16}$')));
    expect(a == b, isFalse);
  });

  test('notifier applies only matching sub_id', () {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final n = c.read(_provider.notifier);

    n.setSubId('s1');
    n.applyDelta(_d('s1', 0, ['a']));
    expect(c.read(_provider).chunks.map((x) => x.id), ['a']);

    n.applyDelta(_d('OTHER', 0, ['x'])); // ignored
    expect(c.read(_provider).chunks.map((x) => x.id), ['a']);
  });

  test('loaded flips true on the first matching delta, even when empty', () {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final n = c.read(_provider.notifier);

    expect(c.read(_provider).loaded, isFalse);
    n.setSubId('s1');
    expect(c.read(_provider).loaded, isFalse); // subscribing, snapshot not in

    n.applyDelta(_d('s1', 0, const [])); // empty snapshot still counts as loaded
    expect(c.read(_provider).loaded, isTrue);
    expect(c.read(_provider).chunks, isEmpty);
  });

  // The detail screen keys transcriptProvider by ClaudeSessionID, which changes on
  // /clear. Distinct keys hold independent chunks, so post-clear never reuses the
  // pre-clear cache.
  test('provider family isolates chunks per key (pre/post-clear)', () {
    final c = ProviderContainer();
    addTearDown(c.dispose);

    final pre = c.read(transcriptProvider('c0').notifier);
    pre.setSubId('s1');
    pre.applyDelta(_d('s1', 0, ['old0', 'old1']));
    expect(c.read(transcriptProvider('c0')).chunks.map((x) => x.id),
        ['old0', 'old1']);

    // New ClaudeSessionID after /clear ⇒ different key ⇒ empty store.
    expect(c.read(transcriptProvider('c1')).chunks, isEmpty);
  });

  test('loaded survives a re-subscribe (reconnect), no spinner flash', () {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final n = c.read(_provider.notifier);

    n.setSubId('s1');
    n.applyDelta(_d('s1', 0, ['a']));
    expect(c.read(_provider).loaded, isTrue);

    n.setSubId('s2'); // reconnect picks a new sub id
    expect(c.read(_provider).loaded, isTrue);
  });
}
