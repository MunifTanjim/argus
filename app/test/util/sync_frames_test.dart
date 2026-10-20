import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/util/sync_frames.dart';

const _begin = '\x1b[?2026h';
const _end = '\x1b[?2026l';

void main() {
  late List<String> writes;
  late SyncFrames f;
  setUp(() {
    writes = [];
    f = SyncFrames((b) => writes.add(utf8.decode(b)));
  });
  tearDown(() => f.dispose());

  test('passes plain output through at once', () {
    f.add(utf8.encode('hello'));
    expect(writes, ['hello']);
  });

  test('writes a redraw split across chunks whole when it ends', () {
    f.add(utf8.encode('a${_begin}top'));
    f.add(utf8.encode('middle'));
    expect(writes, ['a$_begin']);
    f.add(utf8.encode('bottom${_end}b'));
    expect(writes, ['a$_begin', 'topmiddlebottom$_end', 'b']);
  });

  test('finds the brackets when a chunk splits them', () {
    f.add(utf8.encode('x\x1b[?20'));
    expect(writes, ['x']);
    f.add(utf8.encode('26hredraw\x1b[?2'));
    expect(writes, ['x', _begin]);
    f.add(utf8.encode('026l'));
    expect(writes, ['x', _begin, 'redraw$_end']);
  });

  test('releases an update that never ends after the timeout', () async {
    final g = SyncFrames((b) => writes.add(utf8.decode(b)),
        timeout: const Duration(milliseconds: 10));
    g.add(utf8.encode('${_begin}stuck'));
    expect(writes, [_begin]);
    await Future<void>.delayed(const Duration(milliseconds: 50));
    expect(writes, [_begin, 'stuck']);
    g.dispose();
  });

  test('reset drops what a dead attach left held', () {
    f.add(utf8.encode('${_begin}old'));
    f.reset();
    f.add(utf8.encode('new'));
    expect(writes, [_begin, 'new']);
  });
}
