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

    // Modified cursor/nav keys: xterm CSI encoding. Cursor keys use CSI 1;<mod><final>;
    // the ~-style nav keys use CSI <num>;<mod>~. mod = 1 + shift(1) + alt(2) + ctrl(4).
    test('ctrl+home → CSI 1;5H', () => expect(ptyKeyBytes('home', ctrl: true), [27, 91, 49, 59, 53, 72]));
    test('ctrl+end → CSI 1;5F', () => expect(ptyKeyBytes('end', ctrl: true), [27, 91, 49, 59, 53, 70]));
    test('ctrl+up → CSI 1;5A', () => expect(ptyKeyBytes('up', ctrl: true), [27, 91, 49, 59, 53, 65]));
    test('shift+up → CSI 1;2A', () => expect(ptyKeyBytes('up', shift: true), [27, 91, 49, 59, 50, 65]));
    test('alt+left → CSI 1;3D', () => expect(ptyKeyBytes('left', alt: true), [27, 91, 49, 59, 51, 68]));
    test('ctrl+shift+end → CSI 1;6F',
        () => expect(ptyKeyBytes('end', ctrl: true, shift: true), [27, 91, 49, 59, 54, 70]));
    test('ctrl+delete → CSI 3;5~', () => expect(ptyKeyBytes('delete', ctrl: true), [27, 91, 51, 59, 53, 126]));
    test('ctrl+insert → CSI 2;5~', () => expect(ptyKeyBytes('insert', ctrl: true), [27, 91, 50, 59, 53, 126]));
    test('ctrl+pgup → CSI 5;5~', () => expect(ptyKeyBytes('pgup', ctrl: true), [27, 91, 53, 59, 53, 126]));
    test('ctrl+pgdown → CSI 6;5~', () => expect(ptyKeyBytes('pgdown', ctrl: true), [27, 91, 54, 59, 53, 126]));
    // All three modifiers together → param 8 (1 + shift(1) + alt(2) + ctrl(4)).
    test('ctrl+shift+alt+end → CSI 1;8F',
        () => expect(ptyKeyBytes('end', ctrl: true, shift: true, alt: true), [27, 91, 49, 59, 56, 70]));
    // The modifier form wins over DECCKM app-cursor mode.
    test('ctrl+home ignores appCursor',
        () => expect(ptyKeyBytes('home', ctrl: true, appCursor: true), [27, 91, 49, 59, 53, 72]));
    // A modifier on a non-nav key is dropped (unchanged behavior).
    test('ctrl+space stays SP', () => expect(ptyKeyBytes('space', ctrl: true), [32]));
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
