import 'package:argus/ui/live_emulator.dart';
import 'package:argus/ui/theme.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('xterm theme uses the token ANSI colors', () {
    final t = ArgusTokens.fallback;
    final x = xtermThemeFrom(t);
    expect(x.black, t.ansi[0]);
    expect(x.red, t.ansi[1]);
    expect(x.brightWhite, t.ansi[15]);
    expect(x.foreground, t.terminalFg);
    expect(x.background, t.terminalBg);
    expect(x.cursor, t.terminalCursor);
    expect(x.selection, t.terminalSelection);
    expect(x.searchHitBackground, t.ansi[3]);
    expect(x.searchHitBackgroundCurrent, t.ansi[11]);
    expect(x.searchHitForeground, t.ansi[0]);
  });

  test('ghostty theme uses the token ANSI colors', () {
    final t = ArgusTokens.fallback;
    final g = ghosttyThemeFrom(t, fontSize: 15);
    expect(g.palette.ansiColors, t.ansi);
    expect(g.palette.foreground, t.terminalFg);
    expect(g.palette.background, t.terminalBg);
    expect(g.fontSize, 15);
  });
}
