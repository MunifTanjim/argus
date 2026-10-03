import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/state/input_batcher.dart';

void main() {
  testWidgets('joins keys that arrive close together into one send', (tester) async {
    final sent = <String>[];
    final b = InputBatcher((bytes) => sent.add(utf8.decode(bytes)));
    b.add(utf8.encode('l'));
    await tester.pump(const Duration(milliseconds: 10));
    b.add(utf8.encode('s'));
    await tester.pump(const Duration(milliseconds: 10));
    expect(sent, isEmpty);
    await tester.pump(const Duration(milliseconds: 10));
    expect(sent, ['ls']);
  });

  testWidgets('sends at once when the buffer is full', (tester) async {
    final sent = <int>[];
    final b = InputBatcher((bytes) => sent.add(bytes.length), maxBytes: 4);
    b.add(utf8.encode('abcd'));
    expect(sent, [4]);
    b.add(utf8.encode('ef'));
    await tester.pump(const Duration(milliseconds: 20));
    expect(sent, [4, 2]);
  });

  testWidgets('flush sends what is waiting, in order', (tester) async {
    final sent = <String>[];
    final b = InputBatcher((bytes) => sent.add(utf8.decode(bytes)));
    b.add(utf8.encode('a'));
    b.add(utf8.encode('b'));
    b.flush();
    b.flush();
    expect(sent, ['ab']);
    await tester.pump(const Duration(milliseconds: 20));
    expect(sent, ['ab']);
  });
}
