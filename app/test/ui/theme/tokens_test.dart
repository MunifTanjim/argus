import 'package:argus/ui/theme.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

ArgusPalette _withOverrides(ArgusPalette p, PaletteOverrides o) => ArgusPalette(
  name: p.name,
  brightness: p.brightness,
  bg0: p.bg0,
  bg1: p.bg1,
  bg2: p.bg2,
  fg0: p.fg0,
  fg1: p.fg1,
  dim: p.dim,
  red: p.red,
  orange: p.orange,
  yellow: p.yellow,
  green: p.green,
  aqua: p.aqua,
  blue: p.blue,
  purple: p.purple,
  ansi: p.ansi,
  terminalFg: p.terminalFg,
  terminalBg: p.terminalBg,
  overrides: o,
);

void main() {
  test('gruvbox dark tokens keep the original palette values', () {
    final t = ArgusTokens.from(gruvboxDark);
    expect(t.canvas, const Color(0xFF1d2021));
    expect(t.surface, const Color(0xFF282828));
    expect(t.surfaceRaised, const Color(0xFF3c3836));
    expect(t.line, const Color(0xFF3c3836));
    expect(t.text, const Color(0xFFebdbb2));
    expect(t.textSecondary, const Color(0xFFd5c4a1));
    expect(t.textDim, const Color(0xFF928374));
    expect(t.accent, const Color(0xFF8ec07c));
    expect(t.link, const Color(0xFF83a598));
    expect(t.notice, const Color(0xFFfabd2f));
    expect(t.error, const Color(0xFFfb4934));
    expect(t.errorSurface, const Color(0xFF3c1f1d));
    expect(t.destructive, const Color(0xFFfb4934));
    expect(t.blocked, const Color(0xFFfabd2f));
    expect(t.blockedSurface, const Color(0xFF32281d));
    expect(t.blockedBorder, const Color(0xFF7c5f1e));
    expect(t.waiting, const Color(0xFF83a598));
    expect(t.waitingSurface, const Color(0xFF1f2b2c));
    expect(t.waitingBorder, const Color(0xFF45707a));
    expect(t.working, const Color(0xFFb8bb26));
    expect(t.idle, const Color(0xFF928374));
    expect(t.dead, const Color(0xFFfb4934));
    expect(t.diffAdd, const Color(0xFFb8bb26));
    expect(t.diffAddSurface, const Color(0xFFb8bb26).withValues(alpha: 0.10));
    expect(t.diffRemove, const Color(0xFFfb4934));
    expect(
      t.diffRemoveSurface,
      const Color(0xFFfb4934).withValues(alpha: 0.10),
    );
    expect(t.diffHunk, const Color(0xFF83a598));
    expect(t.terminalFg, const Color(0xFFebdbb2));
    expect(t.terminalBg, const Color(0xFF1d2021));
    expect(t.terminalCursor, const Color(0x80ebdbb2));
    expect(t.terminalSelection, const Color(0x40ebdbb2));
    expect(t.keycapFill, t.surfaceRaised);
    expect(t.keycapBorder, t.line);
    expect(t.keycapText, t.textSecondary);
    expect(t.keycapArmed, t.accent);
    expect(t.keycapArmedText, t.canvas);
    expect(t.scrim, const Color(0x8A000000));
    expect(t.success, const Color(0xFFb8bb26));
    expect(t.warning, const Color(0xFFfabd2f));
    expect(t.alert, const Color(0xFFfe8019));
    expect(t.caution, const Color(0xFFd79921));
    expect(t.busy, const Color(0xFFd79921));
    expect(t.workingBadge, const Color(0xFF689d6a));
    expect(t.taskCompleted, const Color(0xFF8ec07c));
    expect(t.taskInProgress, const Color(0xFFfe8019));
    expect(t.thinking, const Color(0xFFd79921));
    expect(t.toolCall, const Color(0xFF458588));
    expect(t.toolRead, const Color(0xFF83a598));
    expect(t.toolEdit, const Color(0xFFfabd2f));
    expect(t.toolWrite, const Color(0xFFb8bb26));
    expect(t.toolShell, const Color(0xFFfe8019));
    expect(t.toolSearch, const Color(0xFFd3869b));
    expect(t.changeAdded, const Color(0xFFb8bb26));
    expect(t.changeModified, const Color(0xFFd79921));
    expect(t.changeDeleted, const Color(0xFFfb4934));
    expect(t.changeRenamed, const Color(0xFFd3869b));
    expect(t.changeUntracked, const Color(0xFF83a598));
    expect(t.commitHash, const Color(0xFFd79921));
    expect(t.syntaxComment, const Color(0xFF665c54));
    expect(t.syntaxTag, const Color(0xFFbdae93));
    expect(t.syntaxKeyword, const Color(0xFFd3869b));
    expect(t.syntaxString, const Color(0xFFb8bb26));
    expect(t.syntaxNumber, const Color(0xFFfe8019));
    expect(t.syntaxTitle, const Color(0xFFfabd2f));
    expect(t.syntaxBuiltIn, const Color(0xFF8ec07c));
    expect(t.syntaxFunction, const Color(0xFF83a598));
    expect(t.syntaxVariable, const Color(0xFFfb4934));
    expect(t.syntaxMeta, const Color(0xFFd65d0e));
    expect(t.ansi, hasLength(16));
    expect(t.ansi.first, const Color(0xFF282828));
    expect(t.ansi[1], const Color(0xFFcc241d));
    expect(t.ansi[2], const Color(0xFF98971a));
    expect(t.ansi[8], const Color(0xFF928374));
    expect(t.ansi[9], const Color(0xFFfb4934));
    expect(t.ansi.last, const Color(0xFFebdbb2));
    expect(t.radius, 6);
    expect(t.density, AppDensity.compact);
    expect(t.statusStyle, StatusStyle.stripe);
  });

  test('an override replaces only its token', () {
    const x = Color(0xFF123456);
    final base = ArgusTokens.from(
      _withOverrides(gruvboxDark, const PaletteOverrides()),
    );
    final t = ArgusTokens.from(
      _withOverrides(gruvboxDark, const PaletteOverrides(accent: x)),
    );
    expect(base.accent, isNot(x));
    expect(t.accent, x);
    expect(t.canvas, base.canvas);
    expect(t.blockedSurface, base.blockedSurface);
    expect(t.error, base.error);
  });

  test('without overrides tokens derive from the base colors', () {
    final t = ArgusTokens.from(
      _withOverrides(gruvboxDark, const PaletteOverrides()),
    );
    expect(t.canvas, gruvboxDark.bg0);
    expect(t.blocked, gruvboxDark.yellow);
    expect(t.keycapFill, t.surfaceRaised);
    expect(t.keycapArmed, t.accent);
    expect(t.blockedSurface, isNot(gruvboxDark.bg0));
  });

  test('accent argument wins over the palette accent', () {
    const x = Color(0xFF654321);
    final t = ArgusTokens.from(
      _withOverrides(
        gruvboxDark,
        const PaletteOverrides(accent: Color(0xFF111111)),
      ),
      accent: x,
    );
    expect(t.accent, x);
    expect(ArgusTokens.from(gruvboxDark, accent: x).accent, x);
  });

  test('copyWith replaces only the given fields', () {
    const x = Color(0xFF123456);
    final base = ArgusTokens.fallback;
    final t = base.copyWith(error: x, radius: 2);
    expect(t.error, x);
    expect(t.radius, 2);
    expect(t.canvas, base.canvas);
    expect(t.density, base.density);
  });

  test('lerp mixes colors and switches non-color fields at half', () {
    final a = ArgusTokens.fallback;
    final b = a.copyWith(
      canvas: const Color(0xFFFFFFFF),
      density: AppDensity.comfortable,
      statusStyle: StatusStyle.dot,
      radius: 12,
    );
    final low = a.lerp(b, 0.25);
    expect(low.canvas, Color.lerp(a.canvas, b.canvas, 0.25));
    expect(low.density, AppDensity.compact);
    expect(low.radius, 6);
    final high = a.lerp(b, 0.5);
    expect(high.density, AppDensity.comfortable);
    expect(high.statusStyle, StatusStyle.dot);
    expect(high.radius, 12);
  });

  test('brand colors keep today\'s hex values', () {
    final t = ArgusTokens.fallback;
    expect(agentBrandColor('claude'), const Color(0xFFfe8019));
    expect(agentBrandColor('codex'), const Color(0xFFb8bb26));
    expect(agentBrandColor('antigravity'), const Color(0xFF83a598));
    expect(agentBrandColor('opencode'), const Color(0xFFebdbb2));
    expect(agentBrandColor('pi'), const Color(0xFFb294bb));
    expect(agentBrandColor('other'), const Color(0xFF928374));
    expect(teamBrandColor('blue', t), const Color(0xFF83a598));
    expect(teamBrandColor('pink', t), const Color(0xFFf7a1b0));
    final custom = t.copyWith(accent: const Color(0xFF123456));
    expect(teamBrandColor(null, custom), custom.accent);
    expect(teamBrandColor('mauve', custom), custom.accent);
  });

  test('theme carries the tokens and keeps today\'s colors', () {
    final theme = buildArgusTheme();
    final t = theme.extension<ArgusTokens>()!;
    expect(t.canvas, ArgusTokens.fallback.canvas);
    expect(theme.scaffoldBackgroundColor, ArgusTokens.fallback.canvas);
    expect(theme.colorScheme.primary, ArgusTokens.fallback.accent);
    expect(theme.colorScheme.surface, ArgusTokens.fallback.surface);
    expect(theme.colorScheme.onSurface, ArgusTokens.fallback.text);
  });

  testWidgets('context.tokens falls back without the extension', (
    tester,
  ) async {
    late ArgusTokens got;
    await tester.pumpWidget(
      MaterialApp(
        home: Builder(
          builder: (context) {
            got = context.tokens;
            return const SizedBox();
          },
        ),
      ),
    );
    expect(identical(got, ArgusTokens.fallback), isTrue);
  });

  group('labels on colored fills pick the more readable of canvas and text', () {
    final palettes = [
      for (final id in ArgusPaletteId.values)
        for (final b in Brightness.values) paletteFor(id, b),
    ];
    for (final p in palettes) {
      test('${p.name} ${p.brightness.name}', () {
        final t = ArgusTokens.from(p);
        for (final (fill, on) in [
          (t.accent, t.onAccent),
          (t.keycapArmed, t.keycapArmedText),
        ]) {
          final got = contrastRatio(on, fill);
          expect(got, greaterThanOrEqualTo(contrastRatio(t.canvas, fill)));
          expect(got, greaterThanOrEqualTo(contrastRatio(t.text, fill)));
        }
      });
    }
  });

  test('nord light labels use text on the accent fill', () {
    final t = ArgusTokens.from(nordLight);
    expect(t.onAccent, t.text);
  });

  testWidgets('context.tokens reads the theme extension', (tester) async {
    final custom = ArgusTokens.from(gruvboxDark, radius: 2);
    late ArgusTokens got;
    await tester.pumpWidget(
      MaterialApp(
        theme: buildArgusTheme().copyWith(extensions: [custom]),
        home: Builder(
          builder: (context) {
            got = context.tokens;
            return const SizedBox();
          },
        ),
      ),
    );
    expect(identical(got, custom), isTrue);
  });
}
