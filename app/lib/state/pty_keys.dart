import 'dart:convert';

// Raw PTY byte sequences for named keys.
const _keySeqs = <String, String>{
  'enter': '\r',
  'tab': '\t',
  'backspace': '\x7f',
  'escape': '\x1b',
  'delete': '\x1b[3~',
  'insert': '\x1b[2~',
  'up': '\x1b[A',
  'down': '\x1b[B',
  'right': '\x1b[C',
  'left': '\x1b[D',
  'home': '\x1b[H',
  'end': '\x1b[F',
  'pgup': '\x1b[5~',
  'pgdown': '\x1b[6~',
  'space': ' ',
};

const _fnKeySeqs = <int, String>{
  1: '\x1bOP', 2: '\x1bOQ', 3: '\x1bOR', 4: '\x1bOS',
  5: '\x1b[15~', 6: '\x1b[17~', 7: '\x1b[18~', 8: '\x1b[19~',
  9: '\x1b[20~', 10: '\x1b[21~', 11: '\x1b[23~', 12: '\x1b[24~',
};

// Application-cursor-key (DECCKM) forms: these six keys emit SS3 (\x1bO) instead
// of CSI (\x1b[) when the remote terminal enabled the mode; the ~-style keys don't.
const _appCursorSeqs = <String, String>{
  'up': '\x1bOA',
  'down': '\x1bOB',
  'right': '\x1bOC',
  'left': '\x1bOD',
  'home': '\x1bOH',
  'end': '\x1bOF',
};

/// Raw PTY bytes for a named key. [shift] affects Tab (back-tab) and Enter;
/// [alt] affects Enter. [appCursor] emits the SS3 form of the cursor keys
/// (DECCKM), which full-screen TUIs enable — pass the remote terminal's
/// `cursorKeysMode`.
List<int> ptyKeyBytes(String name,
    {bool shift = false, bool alt = false, bool appCursor = false}) {
  if (name == 'tab' && shift) return utf8.encode('\x1b[Z');
  // Alt+Enter / Shift+Enter → ESC+CR (meta-Enter): the sequence agent TUIs
  // (Claude Code and other Ink/readline apps) treat as "insert newline".
  if (name == 'enter' && (alt || shift)) return utf8.encode('\x1b\r');
  if (appCursor) {
    final app = _appCursorSeqs[name];
    if (app != null) return utf8.encode(app);
  }
  var seq = _keySeqs[name];
  if (seq == null && name.length >= 2 && name[0] == 'f') {
    final n = int.tryParse(name.substring(1));
    if (n != null) seq = _fnKeySeqs[n];
  }
  return seq == null ? const [] : utf8.encode(seq);
}

// The control byte for a single char, or null if it has no Ctrl form. Covers the
// canonical C0 range: `@`…`_` (0x40–0x5f) and `a`…`z` fold to 0x00–0x1f (so
// Ctrl+A and Ctrl+a are both 0x01, Ctrl+[ is ESC, etc.); Ctrl+Space is NUL and
// Ctrl+? is DEL, matching xterm.
int? _ctrlByte(int c) {
  if (c >= 0x61 && c <= 0x7a) return c - 0x61 + 1; // a-z → 0x01-0x1a
  if (c >= 0x40 && c <= 0x5f) return c - 0x40; // @ A-Z [ \ ] ^ _ → 0x00-0x1f
  if (c == 0x20) return 0x00; // Ctrl+Space → NUL
  if (c == 0x3f) return 0x7f; // Ctrl+? → DEL
  return null;
}

/// Raw PTY bytes for typed text. Ctrl maps a single char to its C0 control byte;
/// a char with no Ctrl form is sent unmodified. Alt prefixes ESC.
List<int> ptyTextBytes(String text, {bool ctrl = false, bool alt = false}) {
  if (text.isEmpty) return const [];
  if (ctrl && text.length == 1) {
    final b = _ctrlByte(text.codeUnitAt(0));
    if (b != null) return alt ? [0x1b, b] : [b];
  }
  final bytes = utf8.encode(text);
  return alt ? [0x1b, ...bytes] : bytes;
}
