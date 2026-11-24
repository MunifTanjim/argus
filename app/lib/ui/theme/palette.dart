import 'package:flutter/material.dart';

/// Per-token overrides a palette applies after deriving its tokens.
class PaletteOverrides {
  const PaletteOverrides({
    this.canvas,
    this.surface,
    this.surfaceRaised,
    this.line,
    this.text,
    this.textSecondary,
    this.textDim,
    this.accent,
    this.onAccent,
    this.link,
    this.notice,
    this.error,
    this.errorSurface,
    this.destructive,
    this.blocked,
    this.blockedSurface,
    this.blockedBorder,
    this.waiting,
    this.waitingSurface,
    this.waitingBorder,
    this.working,
    this.idle,
    this.dead,
    this.diffAdd,
    this.diffAddSurface,
    this.diffRemove,
    this.diffRemoveSurface,
    this.diffHunk,
    this.terminalFg,
    this.terminalBg,
    this.terminalCursor,
    this.terminalSelection,
    this.keycapFill,
    this.keycapBorder,
    this.keycapText,
    this.keycapArmed,
    this.keycapArmedText,
    this.scrim,
    this.success,
    this.warning,
    this.alert,
    this.caution,
    this.busy,
    this.workingBadge,
    this.taskCompleted,
    this.taskInProgress,
    this.thinking,
    this.toolCall,
    this.toolRead,
    this.toolEdit,
    this.toolWrite,
    this.toolShell,
    this.toolSearch,
    this.changeAdded,
    this.changeModified,
    this.changeDeleted,
    this.changeRenamed,
    this.changeUntracked,
    this.commitHash,
    this.syntaxComment,
    this.syntaxTag,
    this.syntaxKeyword,
    this.syntaxString,
    this.syntaxNumber,
    this.syntaxTitle,
    this.syntaxBuiltIn,
    this.syntaxFunction,
    this.syntaxVariable,
    this.syntaxMeta,
  });

  final Color? canvas;
  final Color? surface;
  final Color? surfaceRaised;
  final Color? line;
  final Color? text;
  final Color? textSecondary;
  final Color? textDim;
  final Color? accent;
  final Color? onAccent;
  final Color? link;
  final Color? notice;
  final Color? error;
  final Color? errorSurface;
  final Color? destructive;
  final Color? blocked;
  final Color? blockedSurface;
  final Color? blockedBorder;
  final Color? waiting;
  final Color? waitingSurface;
  final Color? waitingBorder;
  final Color? working;
  final Color? idle;
  final Color? dead;
  final Color? diffAdd;
  final Color? diffAddSurface;
  final Color? diffRemove;
  final Color? diffRemoveSurface;
  final Color? diffHunk;
  final Color? terminalFg;
  final Color? terminalBg;
  final Color? terminalCursor;
  final Color? terminalSelection;
  final Color? keycapFill;
  final Color? keycapBorder;
  final Color? keycapText;
  final Color? keycapArmed;
  final Color? keycapArmedText;
  final Color? scrim;
  final Color? success;
  final Color? warning;
  final Color? alert;
  final Color? caution;
  final Color? busy;
  final Color? workingBadge;
  final Color? taskCompleted;
  final Color? taskInProgress;
  final Color? thinking;
  final Color? toolCall;
  final Color? toolRead;
  final Color? toolEdit;
  final Color? toolWrite;
  final Color? toolShell;
  final Color? toolSearch;
  final Color? changeAdded;
  final Color? changeModified;
  final Color? changeDeleted;
  final Color? changeRenamed;
  final Color? changeUntracked;
  final Color? commitHash;
  final Color? syntaxComment;
  final Color? syntaxTag;
  final Color? syntaxKeyword;
  final Color? syntaxString;
  final Color? syntaxNumber;
  final Color? syntaxTitle;
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
