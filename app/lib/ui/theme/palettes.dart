import 'package:flutter/material.dart';

import 'palette.dart';

/// Identifies a selectable palette family.
enum ArgusPaletteId {
  gruvbox,
  catppuccin,
  tokyoNight,
  nord,
  solarized,
  kanagawa,
}

/// A user-chosen accent hue. [paletteDefault] keeps the palette's own accent.
enum AccentChoice {
  paletteDefault,
  red,
  orange,
  yellow,
  green,
  aqua,
  blue,
  purple,
}

const _gruvboxNeutralYellow = Color(0xFFd79921);

/// Gruvbox dark, hard contrast (https://github.com/morhetz/gruvbox).
const ArgusPalette gruvboxDark = ArgusPalette(
  name: 'Gruvbox',
  brightness: Brightness.dark,
  bg0: Color(0xFF1d2021), // bg0_h
  bg1: Color(0xFF282828), // bg0
  bg2: Color(0xFF3c3836), // bg1
  fg0: Color(0xFFebdbb2), // fg1
  fg1: Color(0xFFd5c4a1), // fg2
  dim: Color(0xFF928374), // gray
  red: Color(0xFFfb4934),
  orange: Color(0xFFfe8019),
  yellow: Color(0xFFfabd2f),
  green: Color(0xFFb8bb26),
  aqua: Color(0xFF8ec07c),
  blue: Color(0xFF83a598),
  purple: Color(0xFFd3869b), // not in Gruvbox; today's team pink
  ansi: [
    Color(0xFF282828),
    Color(0xFFcc241d),
    Color(0xFF98971a),
    Color(0xFFd79921),
    Color(0xFF458588),
    Color(0xFFb16286),
    Color(0xFF689d6a),
    Color(0xFFa89984),
    Color(0xFF928374),
    Color(0xFFfb4934),
    Color(0xFFb8bb26),
    Color(0xFFfabd2f),
    Color(0xFF83a598),
    Color(0xFFd3869b),
    Color(0xFF8ec07c),
    Color(0xFFebdbb2),
  ],
  terminalFg: Color(0xFFebdbb2),
  terminalBg: Color(0xFF1d2021),
  overrides: PaletteOverrides(
    errorSurface: Color(0xFF3c1f1d),
    blockedSurface: Color(0xFF32281d),
    blockedBorder: Color(0xFF7c5f1e),
    waitingSurface: Color(0xFF1f2b2c),
    waitingBorder: Color(0xFF45707a),
    caution: _gruvboxNeutralYellow,
    busy: _gruvboxNeutralYellow,
    workingBadge: Color(0xFF689d6a), // neutral aqua
    thinking: _gruvboxNeutralYellow,
    toolCall: Color(0xFF458588), // neutral blue
    changeModified: _gruvboxNeutralYellow,
    commitHash: _gruvboxNeutralYellow,
    syntaxComment: Color(0xFF665c54),
    syntaxTag: Color(0xFFbdae93),
    syntaxMeta: Color(0xFFd65d0e),
  ),
);

/// Gruvbox light, hard contrast (https://github.com/morhetz/gruvbox).
const ArgusPalette gruvboxLight = ArgusPalette(
  name: 'Gruvbox Light',
  brightness: Brightness.light,
  bg0: Color(0xFFf9f5d7), // light0_hard
  bg1: Color(0xFFfbf1c7), // light0
  bg2: Color(0xFFebdbb2), // light1
  fg0: Color(0xFF3c3836), // dark1 (fg1)
  fg1: Color(0xFF504945), // dark2 (fg2)
  dim: Color(0xFF928374), // gray
  red: Color(0xFF9d0006), // faded red
  orange: Color(0xFFaf3a03),
  yellow: Color(0xFFb57614),
  green: Color(0xFF79740e),
  aqua: Color(0xFF427b58),
  blue: Color(0xFF076678),
  purple: Color(0xFF8f3f71), // not in Gruvbox; faded purple
  ansi: [
    Color(0xFFfbf1c7),
    Color(0xFFcc241d),
    Color(0xFF98971a),
    Color(0xFFd79921),
    Color(0xFF458588),
    Color(0xFFb16286),
    Color(0xFF689d6a),
    Color(0xFF7c6f64),
    Color(0xFF928374),
    Color(0xFF9d0006),
    Color(0xFF79740e),
    Color(0xFFb57614),
    Color(0xFF076678),
    Color(0xFF8f3f71),
    Color(0xFF427b58),
    Color(0xFF3c3836),
  ],
  terminalFg: Color(0xFF3c3836),
  terminalBg: Color(0xFFf9f5d7),
);

/// Catppuccin Mocha (https://github.com/catppuccin/palette).
const ArgusPalette catppuccinMocha = ArgusPalette(
  name: 'Catppuccin Mocha',
  brightness: Brightness.dark,
  bg0: Color(0xFF1e1e2e), // base
  bg1: Color(0xFF313244), // surface0
  bg2: Color(0xFF45475a), // surface1
  fg0: Color(0xFFcdd6f4), // text
  fg1: Color(0xFFbac2de), // subtext1
  dim: Color(0xFF7f849c), // overlay1
  red: Color(0xFFf38ba8),
  orange: Color(0xFFfab387), // peach
  yellow: Color(0xFFf9e2af),
  green: Color(0xFFa6e3a1),
  aqua: Color(0xFF94e2d5), // teal
  blue: Color(0xFF89b4fa),
  purple: Color(0xFFcba6f7), // mauve
  ansi: [
    Color(0xFF45475a),
    Color(0xFFf38ba8),
    Color(0xFFa6e3a1),
    Color(0xFFf9e2af),
    Color(0xFF89b4fa),
    Color(0xFFf5c2e7),
    Color(0xFF94e2d5),
    Color(0xFFa6adc8),
    Color(0xFF585b70),
    Color(0xFFf37799),
    Color(0xFF89d88b),
    Color(0xFFebd391),
    Color(0xFF74a8fc),
    Color(0xFFf2aede),
    Color(0xFF6bd7ca),
    Color(0xFFbac2de),
  ],
  terminalFg: Color(0xFFcdd6f4),
  terminalBg: Color(0xFF1e1e2e),
  overrides: PaletteOverrides(
    syntaxComment: Color(0xFF9399b2), // overlay2
  ),
);

/// Catppuccin Latte (https://github.com/catppuccin/palette).
const ArgusPalette catppuccinLatte = ArgusPalette(
  name: 'Catppuccin Latte',
  brightness: Brightness.light,
  bg0: Color(0xFFeff1f5), // base
  bg1: Color(0xFFe6e9ef), // mantle, Catppuccin's pane color
  bg2: Color(0xFFbcc0cc), // surface1
  fg0: Color(0xFF4c4f69), // text
  fg1: Color(0xFF5c5f77), // subtext1
  dim: Color(0xFF8c8fa1), // overlay1
  red: Color(0xFFd20f39),
  orange: Color(0xFFfe640b), // peach
  yellow: Color(0xFFdf8e1d),
  green: Color(0xFF40a02b),
  aqua: Color(0xFF179299), // teal
  blue: Color(0xFF1e66f5),
  purple: Color(0xFF8839ef), // mauve
  ansi: [
    Color(0xFF5c5f77),
    Color(0xFFd20f39),
    Color(0xFF40a02b),
    Color(0xFFdf8e1d),
    Color(0xFF1e66f5),
    Color(0xFFea76cb),
    Color(0xFF179299),
    Color(0xFFacb0be),
    Color(0xFF6c6f85),
    Color(0xFFde293e),
    Color(0xFF49af3d),
    Color(0xFFeea02d),
    Color(0xFF456eff),
    Color(0xFFfe85d8),
    Color(0xFF2d9fa8),
    Color(0xFFbcc0cc),
  ],
  terminalFg: Color(0xFF4c4f69),
  terminalBg: Color(0xFFeff1f5),
  overrides: PaletteOverrides(
    textDim: Color(0xFF6c6f85), // subtext0
    syntaxComment: Color(0xFF7c7f93), // overlay2
  ),
);

/// Tokyo Night, night style (https://github.com/folke/tokyonight.nvim).
const ArgusPalette tokyoNight = ArgusPalette(
  name: 'Tokyo Night',
  brightness: Brightness.dark,
  bg0: Color(0xFF1a1b26), // bg
  bg1: Color(0xFF292e42), // bg_highlight
  bg2: Color(0xFF3b4261), // fg_gutter
  fg0: Color(0xFFc0caf5), // fg
  fg1: Color(0xFFa9b1d6), // fg_dark
  dim: Color(0xFF565f89), // comment
  red: Color(0xFFf7768e),
  orange: Color(0xFFff9e64),
  yellow: Color(0xFFe0af68),
  green: Color(0xFF9ece6a),
  aqua: Color(0xFF7dcfff), // cyan
  blue: Color(0xFF7aa2f7),
  purple: Color(0xFFbb9af7), // magenta // magenta2
  ansi: [
    Color(0xFF15161e),
    Color(0xFFf7768e),
    Color(0xFF9ece6a),
    Color(0xFFe0af68),
    Color(0xFF7aa2f7),
    Color(0xFFbb9af7),
    Color(0xFF7dcfff),
    Color(0xFFa9b1d6),
    Color(0xFF414868),
    Color(0xFFff899d),
    Color(0xFF9fe044),
    Color(0xFFfaba4a),
    Color(0xFF8db0ff),
    Color(0xFFc7a9ff),
    Color(0xFFa4daff),
    Color(0xFFc0caf5),
  ],
  terminalFg: Color(0xFFc0caf5),
  terminalBg: Color(0xFF1a1b26),
  overrides: PaletteOverrides(
    textDim: Color(0xFF737aa2), // dark5
    syntaxKeyword: Color(0xFF9d7cd8), // purple
  ),
);

/// Tokyo Night, day style (https://github.com/folke/tokyonight.nvim).
const ArgusPalette tokyoNightDay = ArgusPalette(
  name: 'Tokyo Night Day',
  brightness: Brightness.light,
  bg0: Color(0xFFe1e2e7), // bg
  bg1: Color(0xFFd0d5e3), // bg_dark, the sidebar and float color
  bg2: Color(0xFFa8aecb), // fg_gutter
  fg0: Color(0xFF3760bf), // fg
  fg1: Color(0xFF6172b0), // fg_dark
  dim: Color(0xFF848cb5), // comment
  red: Color(0xFFf52a65),
  orange: Color(0xFFb15c00),
  yellow: Color(0xFF8c6c3e),
  green: Color(0xFF587539),
  aqua: Color(0xFF007197), // cyan
  blue: Color(0xFF2e7de9),
  purple: Color(0xFF9854f1), // magenta // magenta2
  ansi: [
    Color(0xFFb4b5b9),
    Color(0xFFf52a65),
    Color(0xFF587539),
    Color(0xFF8c6c3e),
    Color(0xFF2e7de9),
    Color(0xFF9854f1),
    Color(0xFF007197),
    Color(0xFF6172b0),
    Color(0xFFa1a6c5),
    Color(0xFFff4774),
    Color(0xFF5c8524),
    Color(0xFFa27629),
    Color(0xFF358aff),
    Color(0xFFa463ff),
    Color(0xFF007ea8),
    Color(0xFF3760bf),
  ],
  terminalFg: Color(0xFF3760bf),
  terminalBg: Color(0xFFe1e2e7),
  overrides: PaletteOverrides(
    textDim: Color(0xFF68709a), // dark5
    syntaxKeyword: Color(0xFF7847bd), // purple
  ),
);

// Nord (https://www.nordtheme.com/docs/colors-and-palettes).
const _nord0 = Color(0xFF2e3440);
const _nord1 = Color(0xFF3b4252);
const _nord2 = Color(0xFF434c5e);
const _nord3 = Color(0xFF4c566a);
const _nord4 = Color(0xFFd8dee9);
const _nord5 = Color(0xFFe5e9f0);
const _nord6 = Color(0xFFeceff4);
const _nord7 = Color(0xFF8fbcbb);
const _nord8 = Color(0xFF88c0d0);
const _nord9 = Color(0xFF81a1c1);
const _nord10 = Color(0xFF5e81ac);
const _nord11 = Color(0xFFbf616a);
const _nord12 = Color(0xFFd08770);
const _nord13 = Color(0xFFebcb8b);
const _nord14 = Color(0xFFa3be8c);
const _nord15 = Color(0xFFb48ead);

/// Nord's terminal colors (nord-alacritty, nord-iterm2).
const List<Color> _nordAnsi = [
  _nord1,
  _nord11,
  _nord14,
  _nord13,
  _nord9,
  _nord15,
  _nord8,
  _nord5,
  _nord3,
  _nord11,
  _nord14,
  _nord13,
  _nord9,
  _nord15,
  _nord7,
  _nord6,
];

/// Nord's syntax roles: types nord7, functions nord8, keywords nord9,
/// preprocessor nord10, numbers nord15.
const _nordSyntax = PaletteOverrides(
  syntaxBuiltIn: _nord7,
  syntaxFunction: _nord8,
  syntaxKeyword: _nord9,
  syntaxMeta: _nord10,
  syntaxNumber: _nord15,
);

/// Nord (https://www.nordtheme.com).
const ArgusPalette nord = ArgusPalette(
  name: 'Nord',
  brightness: Brightness.dark,
  bg0: _nord0,
  bg1: _nord1,
  bg2: _nord2,
  fg0: _nord6,
  fg1: _nord4,
  // nord3 bright, the comment color of Nord's editor ports.
  dim: Color(0xFF616e88),
  red: _nord11,
  orange: _nord12,
  yellow: _nord13,
  green: _nord14,
  aqua: _nord8,
  blue: _nord9,
  purple: _nord15, // not in Nord
  ansi: _nordAnsi,
  terminalFg: _nord4,
  terminalBg: _nord0,
  overrides: _nordSyntax,
);

// Nord ships no light theme, and its Frost and Aurora hues are too light to
// read on Snow Storm. These keep each hue and saturation, with the lightness
// lowered until the color reaches 3:1 on nord5. nord10 and nord11 already do.
const _nord7OnSnow = Color(0xFF568f8e);
const _nord8OnSnow = Color(0xFF418ea4);
const _nord12OnSnow = Color(0xFFc66d51);
const _nord13OnSnow = Color(0xFFad7d1e);
const _nord14OnSnow = Color(0xFF6d8e50);
const _nord15OnSnow = Color(0xFFa5779c);

/// Nord's terminal colors for a Snow Storm background. This mapping is ours:
/// black and white swap to Snow Storm and Polar Night, and the hues use the
/// darker variants above.
const List<Color> _nordLightAnsi = [
  _nord5,
  _nord11,
  _nord14OnSnow,
  _nord13OnSnow,
  _nord10,
  _nord15OnSnow,
  _nord8OnSnow,
  _nord3,
  _nord4,
  _nord11,
  _nord14OnSnow,
  _nord13OnSnow,
  _nord10,
  _nord15OnSnow,
  _nord7OnSnow,
  _nord0,
];

/// Nord's syntax roles, with the darker variants for Snow Storm.
const _nordLightSyntax = PaletteOverrides(
  syntaxBuiltIn: _nord7OnSnow,
  syntaxFunction: _nord8OnSnow,
  syntaxKeyword: _nord10,
  syntaxMeta: _nord10,
  syntaxNumber: _nord15OnSnow,
);

/// Nord on Snow Storm (https://www.nordtheme.com).
const ArgusPalette nordLight = ArgusPalette(
  name: 'Nord Light',
  brightness: Brightness.light,
  bg0: _nord6,
  bg1: _nord5,
  bg2: _nord4,
  fg0: _nord0,
  fg1: _nord1,
  dim: _nord3,
  red: _nord11,
  orange: _nord12OnSnow,
  yellow: _nord13OnSnow,
  green: _nord14OnSnow,
  aqua: _nord8OnSnow,
  blue: _nord10,
  purple: _nord15OnSnow, // not in Nord
  ansi: _nordLightAnsi,
  terminalFg: _nord0,
  terminalBg: _nord6,
  overrides: _nordLightSyntax,
);

// Solarized (https://ethanschoonover.com/solarized/).
const _base03 = Color(0xFF002b36);
const _base02 = Color(0xFF073642);
const _base01 = Color(0xFF586e75);
const _base00 = Color(0xFF657b83);
const _base0 = Color(0xFF839496);
const _base1 = Color(0xFF93a1a1);
const _base2 = Color(0xFFeee8d5);
const _base3 = Color(0xFFfdf6e3);
const _solYellow = Color(0xFFb58900);
const _solOrange = Color(0xFFcb4b16);
const _solRed = Color(0xFFdc322f);
const _solMagenta = Color(0xFFd33682);
const _solViolet = Color(0xFF6c71c4);
const _solBlue = Color(0xFF268bd2);
const _solCyan = Color(0xFF2aa198);
const _solGreen = Color(0xFF859900);

/// Solarized's terminal colors, shared by dark and light.
const List<Color> _solarizedAnsi = [
  _base02,
  _solRed,
  _solGreen,
  _solYellow,
  _solBlue,
  _solMagenta,
  _solCyan,
  _base2,
  _base03,
  _solOrange,
  _base01,
  _base00,
  _base0,
  _solViolet,
  _base1,
  _base3,
];

/// Solarized dark (https://ethanschoonover.com/solarized/).
const ArgusPalette solarizedDark = ArgusPalette(
  name: 'Solarized Dark',
  brightness: Brightness.dark,
  bg0: _base03,
  bg1: _base02,
  bg2: _base01,
  fg0: _base1,
  fg1: _base0,
  dim: _base01,
  red: _solRed,
  orange: _solOrange,
  yellow: _solYellow,
  green: _solGreen,
  aqua: _solCyan,
  blue: _solBlue,
  purple: _solViolet,
  ansi: _solarizedAnsi,
  terminalFg: _base0,
  terminalBg: _base03,
  overrides: PaletteOverrides(
    // Solarized has two background tones; base01 is a content tone, fine for
    // lines but not as a fill under text.
    surfaceRaised: _base02,
    // Solarized vim groups: Statement green, Constant cyan, Type yellow,
    // Identifier blue.
    syntaxKeyword: _solGreen,
    syntaxString: _solCyan,
    syntaxNumber: _solCyan,
    syntaxBuiltIn: _solYellow,
    syntaxVariable: _solBlue,
  ),
);

/// Solarized light (https://ethanschoonover.com/solarized/).
const ArgusPalette solarizedLight = ArgusPalette(
  name: 'Solarized Light',
  brightness: Brightness.light,
  bg0: _base3,
  bg1: _base2,
  bg2: _base1,
  fg0: _base01,
  fg1: _base00,
  dim: _base1,
  red: _solRed,
  orange: _solOrange,
  yellow: _solYellow,
  green: _solGreen,
  aqua: _solCyan,
  blue: _solBlue,
  purple: _solViolet,
  ansi: _solarizedAnsi,
  terminalFg: _base00,
  terminalBg: _base3,
  overrides: PaletteOverrides(
    surfaceRaised: _base2,
    // Solarized vim groups: Statement green, Constant cyan, Type yellow,
    // Identifier blue.
    syntaxKeyword: _solGreen,
    syntaxString: _solCyan,
    syntaxNumber: _solCyan,
    syntaxBuiltIn: _solYellow,
    syntaxVariable: _solBlue,
  ),
);

// Kanagawa (https://github.com/rebelot/kanagawa.nvim). Roles follow the
// wave and lotus themes in lua/kanagawa/themes.lua.

/// Kanagawa wave (https://github.com/rebelot/kanagawa.nvim).
const ArgusPalette kanagawaWave = ArgusPalette(
  name: 'Kanagawa Wave',
  brightness: Brightness.dark,
  bg0: Color(0xFF1f1f28), // sumiInk3 (bg)
  bg1: Color(0xFF2a2a37), // sumiInk4 (bg_p1)
  bg2: Color(0xFF363646), // sumiInk5 (bg_p2)
  fg0: Color(0xFFdcd7ba), // fujiWhite (fg)
  fg1: Color(0xFFc8c093), // oldWhite (fg_dim)
  dim: Color(0xFF727169), // fujiGray (comment)
  red: Color(0xFFe46876), // waveRed
  orange: Color(0xFFffa066), // surimiOrange
  yellow: Color(0xFFe6c384), // carpYellow
  green: Color(0xFF98bb6c), // springGreen
  aqua: Color(0xFF7aa89f), // waveAqua2
  blue: Color(0xFF7e9cd8), // crystalBlue
  purple: Color(0xFF957fb8), // oniViolet // sakuraPink
  ansi: [
    Color(0xFF16161d), // sumiInk0
    Color(0xFFc34043), // autumnRed
    Color(0xFF76946a), // autumnGreen
    Color(0xFFc0a36e), // boatYellow2
    Color(0xFF7e9cd8), // crystalBlue
    Color(0xFF957fb8), // oniViolet
    Color(0xFF6a9589), // waveAqua1
    Color(0xFFc8c093), // oldWhite
    Color(0xFF727169), // fujiGray
    Color(0xFFe82424), // samuraiRed
    Color(0xFF98bb6c), // springGreen
    Color(0xFFe6c384), // carpYellow
    Color(0xFF7fb4ca), // springBlue
    Color(0xFF938aa9), // springViolet1
    Color(0xFF7aa89f), // waveAqua2
    Color(0xFFdcd7ba), // fujiWhite
  ],
  terminalFg: Color(0xFFdcd7ba),
  terminalBg: Color(0xFF1f1f28),
  overrides: PaletteOverrides(
    textDim: Color(0xFF717c7c), // katanaGray
    warning: Color(0xFFff9e3b), // roninYellow (diag warning)
    changeAdded: Color(0xFF76946a), // autumnGreen (vcs)
    changeModified: Color(0xFFdca561), // autumnYellow (vcs)
    changeDeleted: Color(0xFFc34043), // autumnRed (vcs)
    syntaxNumber: Color(0xFFd27e99), // sakuraPink
    syntaxVariable: Color(0xFFe6c384), // carpYellow (identifier)
    syntaxMeta: Color(0xFFe46876), // waveRed (preproc)
  ),
);

/// Kanagawa lotus (https://github.com/rebelot/kanagawa.nvim).
const ArgusPalette kanagawaLotus = ArgusPalette(
  name: 'Kanagawa Lotus',
  brightness: Brightness.light,
  bg0: Color(0xFFf2ecbc), // lotusWhite3 (bg)
  bg1: Color(0xFFe5ddb0), // lotusWhite2 (bg_m1)
  bg2: Color(0xFFd5cea3), // lotusWhite0 (float bg)
  fg0: Color(0xFF545464), // lotusInk1 (fg)
  // lotusGray2 (float border). Upstream fg_dim, lotusInk2, is darker than fg.
  fg1: Color(0xFF716e61),
  dim: Color(0xFF8a8980), // lotusGray3 (comment)
  red: Color(0xFFc84053), // lotusRed
  orange: Color(0xFFcc6d00), // lotusOrange
  yellow: Color(0xFF77713f), // lotusYellow
  green: Color(0xFF6f894e), // lotusGreen
  aqua: Color(0xFF597b75), // lotusAqua
  blue: Color(0xFF4d699b), // lotusBlue4
  purple: Color(0xFF624c83), // lotusViolet4 // lotusPink
  ansi: [
    Color(0xFF1f1f28), // sumiInk3
    Color(0xFFc84053), // lotusRed
    Color(0xFF6f894e), // lotusGreen
    Color(0xFF77713f), // lotusYellow
    Color(0xFF4d699b), // lotusBlue4
    Color(0xFFb35b79), // lotusPink
    Color(0xFF597b75), // lotusAqua
    Color(0xFF545464), // lotusInk1
    Color(0xFF8a8980), // lotusGray3
    Color(0xFFd7474b), // lotusRed2
    Color(0xFF6e915f), // lotusGreen2
    Color(0xFF836f4a), // lotusYellow2
    Color(0xFF6693bf), // lotusTeal2
    Color(0xFF624c83), // lotusViolet4
    Color(0xFF5e857a), // lotusAqua2
    Color(0xFF43436c), // lotusInk2
  ],
  terminalFg: Color(0xFF545464),
  terminalBg: Color(0xFFf2ecbc),
  overrides: PaletteOverrides(
    changeAdded: Color(0xFF6e915f), // lotusGreen2 (vcs)
    changeDeleted: Color(0xFFd7474b), // lotusRed2 (vcs)
    syntaxNumber: Color(0xFFb35b79), // lotusPink
    syntaxVariable: Color(0xFF77713f), // lotusYellow (identifier)
    syntaxMeta: Color(0xFFc84053), // lotusRed (preproc)
  ),
);

/// The palette for [id] in brightness [b].
ArgusPalette paletteFor(ArgusPaletteId id, Brightness b) {
  final dark = b == Brightness.dark;
  return switch (id) {
    ArgusPaletteId.gruvbox => dark ? gruvboxDark : gruvboxLight,
    ArgusPaletteId.catppuccin => dark ? catppuccinMocha : catppuccinLatte,
    ArgusPaletteId.tokyoNight => dark ? tokyoNight : tokyoNightDay,
    ArgusPaletteId.nord => dark ? nord : nordLight,
    ArgusPaletteId.solarized => dark ? solarizedDark : solarizedLight,
    ArgusPaletteId.kanagawa => dark ? kanagawaWave : kanagawaLotus,
  };
}

/// The hue of [p] that [c] picks, or null to keep the palette's own accent.
Color? accentColor(ArgusPalette p, AccentChoice c) => switch (c) {
  AccentChoice.paletteDefault => null,
  AccentChoice.red => p.red,
  AccentChoice.orange => p.orange,
  AccentChoice.yellow => p.yellow,
  AccentChoice.green => p.green,
  AccentChoice.aqua => p.aqua,
  AccentChoice.blue => p.blue,
  AccentChoice.purple => p.purple,
};
