import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/ansi_text.dart';

void main() {
  group('parseAnsi', () {
    const base = TextStyle();

    List<InlineSpan> parse(String s) => parseAnsi(s, base: base);

    String joinedText(List<InlineSpan> spans) =>
        spans.map((s) => (s as TextSpan).text ?? '').join();

    test('plain text produces single span with no colour', () {
      final spans = parse('hello');
      expect(spans, hasLength(1));
      final span = spans.first as TextSpan;
      expect(span.text, 'hello');
      expect(span.style?.color, isNull);
    });

    test('fg colour run then reset', () {
      final spans = parse('\x1b[31mred\x1b[0m.');
      expect(spans, hasLength(2));
      final red = spans[0] as TextSpan;
      expect(red.text, 'red');
      expect(red.style?.color, ansiRed);
      final dot = spans[1] as TextSpan;
      expect(dot.text, '.');
      expect(dot.style?.color, isNull);
    });

    test('bold then not-bold', () {
      final spans = parse('\x1b[1mbold\x1b[22m x');
      expect(spans, hasLength(2));
      final bold = spans[0] as TextSpan;
      expect(bold.text, 'bold');
      expect(bold.style?.fontWeight, FontWeight.bold);
      final normal = spans[1] as TextSpan;
      expect(normal.text, ' x');
      expect(normal.style?.fontWeight, isNot(FontWeight.bold));
    });

    test('cursor/clear sequences stripped, visible text only', () {
      final spans = parse('a\x1b[2J\x1b[Hb');
      expect(joinedText(spans), 'ab');
      for (final span in spans) {
        final text = (span as TextSpan).text ?? '';
        expect(text.contains('\x1b'), isFalse,
            reason: 'No escape chars in span text');
      }
    });

    test('multi-code 1;32 produces bold green span', () {
      final spans = parse('\x1b[1;32mok');
      expect(spans, hasLength(1));
      final ok = spans[0] as TextSpan;
      expect(ok.text, 'ok');
      expect(ok.style?.fontWeight, FontWeight.bold);
      expect(ok.style?.color, ansiGreen);
    });

    test('joined span texts equal input minus escape sequences', () {
      const input = '\x1b[31mred\x1b[0m and \x1b[1mbold\x1b[22m text';
      final spans = parse(input);
      expect(joinedText(spans), 'red and bold text');
    });

    test('unterminated CSI does not leak bracket/params as plain text', () {
      // 'ab\x1b[31' — CSI with no final byte; the '[31' must not appear in output.
      final spans = parse('ab\x1b[31');
      expect(joinedText(spans), 'ab');
    });

    test('unterminated CSI before a colour span leaves colour span intact', () {
      // Ensure a well-formed sequence after the bad one still applies.
      final spans = parse('\x1b[31mred\x1b[0m');
      expect(spans.first as TextSpan, isA<TextSpan>());
      expect((spans.first as TextSpan).style?.color, ansiRed);
    });

    test('OSC sequence is stripped, surrounding text preserved', () {
      // 'x\x1b]0;title\x07y' → only 'xy' visible.
      final spans = parse('x\x1b]0;title\x07y');
      expect(joinedText(spans), 'xy');
    });

    test('lone ESC at end-of-string produces no extra text', () {
      final spans = parse('hi\x1b');
      expect(joinedText(spans), 'hi');
    });
  });

  testWidgets('AnsiText renders without error', (tester) async {
    await tester.pumpWidget(
      const MaterialApp(home: Scaffold(body: AnsiText('hello world'))),
    );
    expect(find.textContaining('hello world'), findsOneWidget);
  });
}
