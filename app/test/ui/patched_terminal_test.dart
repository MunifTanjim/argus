import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/patched_terminal.dart';

String _row(PatchedTerminal t, int width) => String.fromCharCodes([
      for (var i = 0; i < width; i++)
        t.buffer.lines[0].getCodePoint(i) == 0 ? 0x2e : t.buffer.lines[0].getCodePoint(i),
    ]);

void main() {
  test('erasing left of the cursor at column 0 erases that cell', () {
    final t = PatchedTerminal();
    t.write('abc\r\x1b[1K');
    expect(_row(t, 3), '.bc');
  });

  test('erasing left of the cursor includes the cursor cell', () {
    final t = PatchedTerminal();
    t.write('abcd\x1b[2D\x1b[1K');
    expect(_row(t, 4), '...d');
  });
}
