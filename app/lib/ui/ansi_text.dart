import 'package:flutter/material.dart';

// Standard 16-colour ANSI palette.
// Normal (30-37 / 40-47)
const Color ansiBlack = Color(0xFF000000);
const Color ansiRed = Color(0xFFAA0000);
const Color ansiGreen = Color(0xFF00AA00);
const Color ansiYellow = Color(0xFFAA5500);
const Color ansiBlue = Color(0xFF0000AA);
const Color ansiMagenta = Color(0xFFAA00AA);
const Color ansiCyan = Color(0xFF00AAAA);
const Color ansiWhite = Color(0xFFAAAAAA);
// Bright (90-97 / 100-107)
const Color ansiBrightBlack = Color(0xFF555555);
const Color ansiBrightRed = Color(0xFFFF5555);
const Color ansiBrightGreen = Color(0xFF55FF55);
const Color ansiBrightYellow = Color(0xFFFFFF55);
const Color ansiBrightBlue = Color(0xFF5555FF);
const Color ansiBrightMagenta = Color(0xFFFF55FF);
const Color ansiBrightCyan = Color(0xFF55FFFF);
const Color ansiBrightWhite = Color(0xFFFFFFFF);

/// Maps an SGR colour index (0-7 normal, 8-15 bright) to a [Color].
Color? _indexToColor(int index) {
  const colors = [
    ansiBlack,
    ansiRed,
    ansiGreen,
    ansiYellow,
    ansiBlue,
    ansiMagenta,
    ansiCyan,
    ansiWhite,
    ansiBrightBlack,
    ansiBrightRed,
    ansiBrightGreen,
    ansiBrightYellow,
    ansiBrightBlue,
    ansiBrightMagenta,
    ansiBrightCyan,
    ansiBrightWhite,
  ];
  if (index < 0 || index >= colors.length) return null;
  return colors[index];
}

/// Parses [screen] (which may contain ANSI SGR escape sequences) and returns
/// a list of [InlineSpan]s with styling applied.
///
/// Recognised SGR codes:
/// - 0: reset all
/// - 1: bold
/// - 22: not-bold
/// - 30-37: standard foreground colours
/// - 39: default foreground
/// - 40-47: standard background colours
/// - 49: default background
/// - 90-97: bright foreground colours
/// - 100-107: bright background colours
///
/// All other escape sequences (cursor moves, private sequences, OSC, lone ESC)
/// are stripped and produce no output.
List<InlineSpan> parseAnsi(String screen, {required TextStyle base}) {
  final spans = <InlineSpan>[];

  // Current SGR state.
  Color? fg;
  Color? bg;
  bool bold = false;

  TextStyle currentStyle() {
    return base.copyWith(
      color: fg,
      backgroundColor: bg,
      fontWeight: bold ? FontWeight.bold : FontWeight.normal,
    );
  }

  void emit(String text) {
    if (text.isEmpty) return;
    spans.add(TextSpan(text: text, style: currentStyle()));
  }

  void applySgr(String params) {
    // Empty params or "0" means reset.
    final codes = params.isEmpty
        ? [0]
        : params.split(';').map((s) => int.tryParse(s.trim()) ?? 0).toList();

    for (final code in codes) {
      if (code == 0) {
        fg = null;
        bg = null;
        bold = false;
      } else if (code == 1) {
        bold = true;
      } else if (code == 22) {
        bold = false;
      } else if (code == 39) {
        fg = null;
      } else if (code == 49) {
        bg = null;
      } else if (code >= 30 && code <= 37) {
        fg = _indexToColor(code - 30);
      } else if (code >= 40 && code <= 47) {
        bg = _indexToColor(code - 40);
      } else if (code >= 90 && code <= 97) {
        fg = _indexToColor(code - 90 + 8);
      } else if (code >= 100 && code <= 107) {
        bg = _indexToColor(code - 100 + 8);
      }
      // Unknown codes are ignored.
    }
  }

  int i = 0;
  final buf = StringBuffer();

  while (i < screen.length) {
    final ch = screen[i];

    if (ch != '\x1b') {
      buf.write(ch);
      i++;
      continue;
    }

    // Flush accumulated plain text before processing the escape.
    emit(buf.toString());
    buf.clear();

    // We have an ESC at position i.
    if (i + 1 >= screen.length) {
      // Lone ESC at end — strip it.
      i++;
      break;
    }

    final next = screen[i + 1];

    if (next == '[') {
      // CSI sequence: ESC [ <params> <final>
      // Collect parameter bytes (0x20-0x3F) up to a final byte (0x40-0x7E).
      int j = i + 2;
      while (j < screen.length && screen.codeUnitAt(j) >= 0x20 && screen.codeUnitAt(j) <= 0x3F) {
        j++;
      }
      // j now points at the final byte (or past end).
      if (j < screen.length) {
        final finalByte = screen[j];
        final params = screen.substring(i + 2, j);
        if (finalByte == 'm') {
          // SGR — apply colour/style.
          applySgr(params);
        }
        // All other CSI sequences (H, J, K, A, B, C, D, private '?' prefixes, etc.)
        // are simply stripped.
        i = j + 1;
      } else {
        // Unterminated CSI — consume entire partial sequence, emit nothing.
        i = screen.length;
      }
    } else if (next == ']') {
      // OSC sequence: ESC ] ... BEL (\x07) or ST (ESC \).
      // If unterminated (no BEL/ST before end-of-string), j reaches end and
      // we consume to end, emitting nothing — same as terminated case.
      int j = i + 2;
      while (j < screen.length) {
        if (screen[j] == '\x07') {
          j++;
          break;
        }
        if (screen[j] == '\x1b' && j + 1 < screen.length && screen[j + 1] == '\\') {
          j += 2;
          break;
        }
        j++;
      }
      i = j;
    } else {
      // Lone ESC followed by an unrecognised byte — strip ESC only; the next
      // byte is re-processed by the loop so a following ESC is not swallowed.
      i += 1;
    }
  }

  // Flush any remaining plain text.
  emit(buf.toString());

  return spans;
}

/// A thin widget that renders ANSI-escaped text on a dark background.
class AnsiText extends StatelessWidget {
  const AnsiText(this.screen, {super.key});

  final String screen;

  static const _baseStyle = TextStyle(
    fontFamily: 'monospace',
    fontSize: 12,
    color: Color(0xFFCCCCCC),
  );

  @override
  Widget build(BuildContext context) {
    return Text.rich(
      TextSpan(children: parseAnsi(screen, base: _baseStyle)),
    );
  }
}
