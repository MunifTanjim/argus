import 'package:flutter/material.dart';

/// Per-token overrides a palette applies after deriving its tokens. Holds
/// only the tokens some palette overrides: add a field when a palette needs it.
class PaletteOverrides {
  const PaletteOverrides({
    this.surfaceRaised,
    this.textDim,
    this.accent,
    this.errorSurface,
    this.blockedSurface,
    this.blockedBorder,
    this.waitingSurface,
    this.waitingBorder,
    this.warning,
    this.caution,
    this.busy,
    this.workingBadge,
    this.thinking,
    this.changeAdded,
    this.changeModified,
    this.changeDeleted,
    this.commitHash,
    this.syntaxComment,
    this.syntaxTag,
    this.syntaxKeyword,
    this.syntaxString,
    this.syntaxNumber,
    this.syntaxBuiltIn,
    this.syntaxFunction,
    this.syntaxVariable,
    this.syntaxMeta,
  });

  final Color? surfaceRaised;
  final Color? textDim;
  final Color? accent;
  final Color? errorSurface;
  final Color? blockedSurface;
  final Color? blockedBorder;
  final Color? waitingSurface;
  final Color? waitingBorder;
  final Color? warning;
  final Color? caution;
  final Color? busy;
  final Color? workingBadge;
  final Color? thinking;
  final Color? changeAdded;
  final Color? changeModified;
  final Color? changeDeleted;
  final Color? commitHash;
  final Color? syntaxComment;
  final Color? syntaxTag;
  final Color? syntaxKeyword;
  final Color? syntaxString;
  final Color? syntaxNumber;
  final Color? syntaxBuiltIn;
  final Color? syntaxFunction;
  final Color? syntaxVariable;
  final Color? syntaxMeta;
}

/// The base colors of one theme. [ArgusTokens.from] derives every token from
/// these, then applies [overrides].
class ArgusPalette {
  const ArgusPalette({
    required this.name,
    required this.brightness,
    required this.bg0,
    required this.bg1,
    required this.bg2,
    required this.fg0,
    required this.fg1,
    required this.dim,
    required this.red,
    required this.orange,
    required this.yellow,
    required this.green,
    required this.aqua,
    required this.blue,
    required this.purple,
    required this.ansi,
    required this.terminalFg,
    required this.terminalBg,
    this.overrides = const PaletteOverrides(),
  });

  final String name;
  final Brightness brightness;
  final Color bg0, bg1, bg2;
  final Color fg0, fg1, dim;
  final Color red, orange, yellow, green, aqua, blue, purple;

  /// The 16 terminal colors: 8 normal, then 8 bright.
  final List<Color> ansi;
  final Color terminalFg, terminalBg;
  final PaletteOverrides overrides;
}
