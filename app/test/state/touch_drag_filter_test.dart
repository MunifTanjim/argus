import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/state/touch_drag_filter.dart';

String run(TouchDragFilter f, List<String> chunks) =>
    chunks.map((c) => latin1.decode(f.filter(latin1.encode(c)))).join();

void main() {
  test('a tap passes as press and release', () {
    expect(run(TouchDragFilter(), ['\x1b[<0;3;2M', '\x1b[<0;3;2m']), '\x1b[<0;3;2M\x1b[<0;3;2m');
  });

  test('a drag drops its press, motion, and release; wheel ticks pass', () {
    expect(
      run(TouchDragFilter(), ['\x1b[<0;3;2M', '\x1b[<64;1;1M', '\x1b[<32;3;5M', '\x1b[<64;1;1M', '\x1b[<0;3;5m']),
      '\x1b[<64;1;1M\x1b[<64;1;1M',
    );
  });

  test('typed text keeps its order around a pending press', () {
    expect(run(TouchDragFilter(), ['\x1b[<0;1;1M', 'ls']), '\x1b[<0;1;1Mls');
  });

  test('motion with no press passes', () {
    expect(run(TouchDragFilter(), ['\x1b[<35;4;4M']), '\x1b[<35;4;4M');
  });

  test('mouse-only keeps resolved mouse reports and drops other bytes', () {
    final f = TouchDragFilter();
    String mouse(String c) => latin1.decode(f.mouseOnly(latin1.encode(c)));
    expect(mouse('ls\x1b[<0;1;1M'), '');
    expect(mouse('x\x1b[<0;1;1m\x1b[<64;1;1M\x1b[?1;2c'), '\x1b[<0;1;1M\x1b[<0;1;1m\x1b[<64;1;1M');
  });
}
