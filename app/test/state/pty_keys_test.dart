import 'package:flutter_test/flutter_test.dart';
import 'package:argus/state/pty_keys.dart';

void main() {
  group('ptyKeyBytes', () {
    final cases = <String, List<int>>{
      'enter': [13],
      'tab': [9],
      'backspace': [127],
      'escape': [27],
      'up': [27, 91, 65],
      'down': [27, 91, 66],
      'right': [27, 91, 67],
      'left': [27, 91, 68],
      'space': [32],
      'f1': [27, 79, 80],
      'pgup': [27, 91, 53, 126],
    };
    cases.forEach((name, want) {
      test('$name → $want', () => expect(ptyKeyBytes(name), want));
    });

    test('shift+tab is back-tab', () => expect(ptyKeyBytes('tab', shift: true), [27, 91, 90]));
    test('unknown key → empty', () => expect(ptyKeyBytes('nope'), isEmpty));

    // Alt+Enter / Shift+Enter → ESC+CR (meta-Enter): "insert newline" in agent TUIs.
    test('plain enter → CR', () => expect(ptyKeyBytes('enter'), [13]));
    test('alt+enter → ESC CR', () => expect(ptyKeyBytes('enter', alt: true), [27, 13]));
    test('shift+enter → ESC CR', () => expect(ptyKeyBytes('enter', shift: true), [27, 13]));

    // Application-cursor-key mode (DECCKM): arrows and Home/End switch from CSI
    // (\x1b[) to SS3 (\x1bO) when the remote TUI enables it.
    test('appCursor up → SS3 OA', () => expect(ptyKeyBytes('up', appCursor: true), [27, 79, 65]));
    test('appCursor down → SS3 OB', () => expect(ptyKeyBytes('down', appCursor: true), [27, 79, 66]));
    test('appCursor right → SS3 OC', () => expect(ptyKeyBytes('right', appCursor: true), [27, 79, 67]));
    test('appCursor left → SS3 OD', () => expect(ptyKeyBytes('left', appCursor: true), [27, 79, 68]));
    test('appCursor home → SS3 OH', () => expect(ptyKeyBytes('home', appCursor: true), [27, 79, 72]));
    test('appCursor end → SS3 OF', () => expect(ptyKeyBytes('end', appCursor: true), [27, 79, 70]));
    // Non-cursor keys are unaffected by DECCKM.
    test('appCursor leaves pgup as CSI', () => expect(ptyKeyBytes('pgup', appCursor: true), [27, 91, 53, 126]));
    test('appCursor leaves delete as CSI', () => expect(ptyKeyBytes('delete', appCursor: true), [27, 91, 51, 126]));
    // Default (no DECCKM) keeps CSI arrows.
    test('default up stays CSI', () => expect(ptyKeyBytes('up'), [27, 91, 65]));
  });

  group('ptyTextBytes', () {
    test('printable → utf8', () => expect(ptyTextBytes('a'), [97]));
    test('multi-char printable', () => expect(ptyTextBytes('hi'), [104, 105]));
    test('ctrl+c → 0x03', () => expect(ptyTextBytes('c', ctrl: true), [3]));
    test('ctrl+a → 0x01', () => expect(ptyTextBytes('a', ctrl: true), [1]));
    test('ctrl+C (upper) → 0x03', () => expect(ptyTextBytes('C', ctrl: true), [3]));
    test('alt+x → ESC x', () => expect(ptyTextBytes('x', alt: true), [27, 120]));
    test('empty → empty', () => expect(ptyTextBytes(''), isEmpty));

    // Ctrl for non-letter chars: the canonical C0 controls.
    test('ctrl+space → NUL', () => expect(ptyTextBytes(' ', ctrl: true), [0]));
    test('ctrl+@ → NUL', () => expect(ptyTextBytes('@', ctrl: true), [0]));
    test('ctrl+[ → ESC', () => expect(ptyTextBytes('[', ctrl: true), [27]));
    test('ctrl+\\ → FS', () => expect(ptyTextBytes('\\', ctrl: true), [28]));
    test('ctrl+] → GS', () => expect(ptyTextBytes(']', ctrl: true), [29]));
    test('ctrl+? → DEL', () => expect(ptyTextBytes('?', ctrl: true), [127]));
    test('ctrl+alt+[ → ESC ESC', () => expect(ptyTextBytes('[', ctrl: true, alt: true), [27, 27]));
    // A char with no Ctrl form is sent unmodified (Ctrl dropped).
    test('ctrl+digit falls through', () => expect(ptyTextBytes('5', ctrl: true), [53]));
  });
}
