import 'package:flutter/material.dart';

import 'palette.dart';
import 'palettes.dart';

enum AppDensity { compact, comfortable }

enum StatusStyle { stripe, dot, filled }

const _scrim = Color(0x8A000000);

Color _mix(Color a, Color b, double k) => Color.lerp(a, b, k)!;

/// WCAG contrast ratio between [a] and [b], from 1 to 21.
double contrastRatio(Color a, Color b) {
  final la = a.computeLuminance();
  final lb = b.computeLuminance();
  return (la > lb ? la + 0.05 : lb + 0.05) / (la > lb ? lb + 0.05 : la + 0.05);
}

/// Whichever of [x] and [y] reads better on [fill].
Color moreReadableOn(Color fill, Color x, Color y) =>
    contrastRatio(x, fill) >= contrastRatio(y, fill) ? x : y;

/// Semantic colors and layout settings, named by what they are for.
class ArgusTokens extends ThemeExtension<ArgusTokens> {
  const ArgusTokens({
    required this.canvas,
    required this.surface,
    required this.surfaceRaised,
    required this.line,
    required this.text,
    required this.textSecondary,
    required this.textDim,
    required this.accent,
    required this.onAccent,
    required this.link,
    required this.notice,
    required this.error,
    required this.errorSurface,
    required this.destructive,
    required this.blocked,
    required this.blockedSurface,
    required this.blockedBorder,
    required this.waiting,
    required this.waitingSurface,
    required this.waitingBorder,
    required this.working,
    required this.idle,
    required this.dead,
    required this.diffAdd,
    required this.diffAddSurface,
    required this.diffRemove,
    required this.diffRemoveSurface,
    required this.diffHunk,
    required this.terminalFg,
    required this.terminalBg,
    required this.terminalCursor,
    required this.terminalSelection,
    required this.keycapFill,
    required this.keycapBorder,
    required this.keycapText,
    required this.keycapArmed,
    required this.keycapArmedText,
    required this.scrim,
    required this.success,
    required this.warning,
    required this.alert,
    required this.caution,
    required this.busy,
    required this.workingBadge,
    required this.taskCompleted,
    required this.taskInProgress,
    required this.thinking,
    required this.toolRead,
    required this.toolEdit,
    required this.toolWrite,
    required this.toolShell,
    required this.toolSearch,
    required this.changeAdded,
    required this.changeModified,
    required this.changeDeleted,
    required this.changeRenamed,
    required this.changeUntracked,
    required this.commitHash,
    required this.syntaxComment,
    required this.syntaxTag,
    required this.syntaxKeyword,
    required this.syntaxString,
    required this.syntaxNumber,
    required this.syntaxTitle,
    required this.syntaxBuiltIn,
    required this.syntaxFunction,
    required this.syntaxVariable,
    required this.syntaxMeta,
    required this.ansi,
    required this.radius,
    required this.density,
    required this.statusStyle,
  });

  /// Derives the tokens from [p]'s base colors, then applies [p]'s overrides.
  /// [accent] wins over both.
  factory ArgusTokens.from(
    ArgusPalette p, {
    AppDensity density = AppDensity.compact,
    StatusStyle statusStyle = StatusStyle.stripe,
    double radius = 6,
    Color? accent,
  }) {
    final o = p.overrides;
    final acc = accent ?? o.accent ?? p.aqua;
    final canvas = o.canvas ?? p.bg0;
    final surface = o.surface ?? p.bg1;
    final surfaceRaised = o.surfaceRaised ?? p.bg2;
    final line = o.line ?? p.bg2;
    final text = o.text ?? p.fg0;
    final textSecondary = o.textSecondary ?? p.fg1;
    final textDim = o.textDim ?? p.dim;
    final link = o.link ?? p.blue;
    final notice = o.notice ?? p.yellow;
    final error = o.error ?? p.red;
    final errorSurface = o.errorSurface ?? _mix(p.bg0, p.red, 0.12);
    final destructive = o.destructive ?? p.red;
    final blocked = o.blocked ?? p.yellow;
    final onAccent = o.onAccent ?? moreReadableOn(acc, canvas, text);
    final blockedSurface = o.blockedSurface ?? _mix(p.bg0, p.yellow, 0.10);
    final blockedBorder = o.blockedBorder ?? _mix(p.bg0, p.yellow, 0.45);
    final waiting = o.waiting ?? p.blue;
    final waitingSurface = o.waitingSurface ?? _mix(p.bg0, p.blue, 0.10);
    final waitingBorder = o.waitingBorder ?? _mix(p.bg0, p.blue, 0.45);
    final working = o.working ?? p.green;
    final idle = o.idle ?? p.dim;
    final dead = o.dead ?? p.red;
    final diffAdd = o.diffAdd ?? p.green;
    final diffAddSurface = o.diffAddSurface ?? p.green.withValues(alpha: 0.10);
    final diffRemove = o.diffRemove ?? p.red;
    final diffRemoveSurface =
        o.diffRemoveSurface ?? p.red.withValues(alpha: 0.10);
    final diffHunk = o.diffHunk ?? p.blue;
    final terminalFg = o.terminalFg ?? p.terminalFg;
    final terminalBg = o.terminalBg ?? p.terminalBg;
    final terminalCursor = o.terminalCursor ?? p.terminalFg.withAlpha(0x80);
    final terminalSelection =
        o.terminalSelection ?? p.terminalFg.withAlpha(0x40);
    final keycapFill = o.keycapFill ?? surfaceRaised;
    final keycapBorder = o.keycapBorder ?? line;
    final keycapText = o.keycapText ?? textSecondary;
    final keycapArmed = o.keycapArmed ?? acc;
    final keycapArmedText = o.keycapArmedText ?? onAccent;
    final scrim = o.scrim ?? _scrim;
    final success = o.success ?? p.green;
    final warning = o.warning ?? p.yellow;
    final alert = o.alert ?? p.orange;
    final caution = o.caution ?? p.yellow;
    final busy = o.busy ?? p.yellow;
    final workingBadge = o.workingBadge ?? p.green;
    final taskCompleted = o.taskCompleted ?? p.aqua;
    final taskInProgress = o.taskInProgress ?? p.orange;
    final thinking = o.thinking ?? p.yellow;
    final toolRead = o.toolRead ?? p.blue;
    final toolEdit = o.toolEdit ?? p.yellow;
    final toolWrite = o.toolWrite ?? p.green;
    final toolShell = o.toolShell ?? p.orange;
    final toolSearch = o.toolSearch ?? p.purple;
    final changeAdded = o.changeAdded ?? p.green;
    final changeModified = o.changeModified ?? p.yellow;
    final changeDeleted = o.changeDeleted ?? p.red;
    final changeRenamed = o.changeRenamed ?? p.purple;
    final changeUntracked = o.changeUntracked ?? p.blue;
    final commitHash = o.commitHash ?? p.yellow;
    final syntaxComment = o.syntaxComment ?? p.dim;
    final syntaxTag = o.syntaxTag ?? p.fg1;
    final syntaxKeyword = o.syntaxKeyword ?? p.purple;
    final syntaxString = o.syntaxString ?? p.green;
    final syntaxNumber = o.syntaxNumber ?? p.orange;
    final syntaxTitle = o.syntaxTitle ?? p.yellow;
    final syntaxBuiltIn = o.syntaxBuiltIn ?? p.aqua;
    final syntaxFunction = o.syntaxFunction ?? p.blue;
    final syntaxVariable = o.syntaxVariable ?? p.red;
    final syntaxMeta = o.syntaxMeta ?? p.orange;
    return ArgusTokens(
      canvas: canvas,
      surface: surface,
      surfaceRaised: surfaceRaised,
      line: line,
      text: text,
      textSecondary: textSecondary,
      textDim: textDim,
      accent: acc,
      onAccent: onAccent,
      link: link,
      notice: notice,
      error: error,
      errorSurface: errorSurface,
      destructive: destructive,
      blocked: blocked,
      blockedSurface: blockedSurface,
      blockedBorder: blockedBorder,
      waiting: waiting,
      waitingSurface: waitingSurface,
      waitingBorder: waitingBorder,
      working: working,
      idle: idle,
      dead: dead,
      diffAdd: diffAdd,
      diffAddSurface: diffAddSurface,
      diffRemove: diffRemove,
      diffRemoveSurface: diffRemoveSurface,
      diffHunk: diffHunk,
      terminalFg: terminalFg,
      terminalBg: terminalBg,
      terminalCursor: terminalCursor,
      terminalSelection: terminalSelection,
      keycapFill: keycapFill,
      keycapBorder: keycapBorder,
      keycapText: keycapText,
      keycapArmed: keycapArmed,
      keycapArmedText: keycapArmedText,
      scrim: scrim,
      success: success,
      warning: warning,
      alert: alert,
      caution: caution,
      busy: busy,
      workingBadge: workingBadge,
      taskCompleted: taskCompleted,
      taskInProgress: taskInProgress,
      thinking: thinking,
      toolRead: toolRead,
      toolEdit: toolEdit,
      toolWrite: toolWrite,
      toolShell: toolShell,
      toolSearch: toolSearch,
      changeAdded: changeAdded,
      changeModified: changeModified,
      changeDeleted: changeDeleted,
      changeRenamed: changeRenamed,
      changeUntracked: changeUntracked,
      commitHash: commitHash,
      syntaxComment: syntaxComment,
      syntaxTag: syntaxTag,
      syntaxKeyword: syntaxKeyword,
      syntaxString: syntaxString,
      syntaxNumber: syntaxNumber,
      syntaxTitle: syntaxTitle,
      syntaxBuiltIn: syntaxBuiltIn,
      syntaxFunction: syntaxFunction,
      syntaxVariable: syntaxVariable,
      syntaxMeta: syntaxMeta,
      ansi: List.unmodifiable(p.ansi),
      radius: radius,
      density: density,
      statusStyle: statusStyle,
    );
  }

  static final ArgusTokens fallback = ArgusTokens.from(gruvboxDark);

  /// Screen background.
  final Color canvas;

  /// Cards and sheets.
  final Color surface;

  /// Controls raised above a surface.
  final Color surfaceRaised;

  /// Borders and dividers.
  final Color line;

  /// Primary text.
  final Color text;

  /// Secondary text and labels.
  final Color textSecondary;

  /// Hints, timestamps, inactive icons.
  final Color textDim;

  /// Non-status chrome: selection, primary actions.
  final Color accent;

  /// Text and icons on an [accent] fill.
  final Color onAccent;

  /// Branches, paths, links.
  final Color link;

  /// Prompt notices.
  final Color notice;

  /// Errors and failed states.
  final Color error;

  /// Background behind an error.
  final Color errorSurface;

  /// Destructive action buttons (kill, delete).
  final Color destructive;

  /// Session blocked on a prompt.
  final Color blocked;

  /// Background of a blocked card.
  final Color blockedSurface;

  /// Border of a blocked card.
  final Color blockedBorder;

  /// Session finished its turn.
  final Color waiting;

  /// Background of a waiting card.
  final Color waitingSurface;

  /// Border of a waiting card.
  final Color waitingBorder;

  /// Session working.
  final Color working;

  /// Session idle, starting, or unknown.
  final Color idle;

  /// Session dead.
  final Color dead;

  /// Added diff lines.
  final Color diffAdd;

  /// Background of added diff lines.
  final Color diffAddSurface;

  /// Removed diff lines.
  final Color diffRemove;

  /// Background of removed diff lines.
  final Color diffRemoveSurface;

  /// Hunk headers and fold bars.
  final Color diffHunk;

  /// Terminal default foreground.
  final Color terminalFg;

  /// Terminal default background.
  final Color terminalBg;

  /// Terminal block cursor.
  final Color terminalCursor;

  /// Terminal selection.
  final Color terminalSelection;

  /// Live-screen key fill.
  final Color keycapFill;

  /// Live-screen key border.
  final Color keycapBorder;

  /// Live-screen key label.
  final Color keycapText;

  /// Armed modifier key fill.
  final Color keycapArmed;

  /// Armed modifier key label.
  final Color keycapArmedText;

  /// Modal barrier over content.
  final Color scrim;

  /// Positive state: authorized, low usage.
  final Color success;

  /// Needs attention: pending trust, high usage.
  final Color warning;

  /// Security alert: trust-log equivocation.
  final Color alert;

  /// Item missing something it needs (dangling profile).
  final Color caution;

  /// Background operation running (workspace setup).
  final Color busy;

  /// Fill of the working-count badge.
  final Color workingBadge;

  /// Completed task.
  final Color taskCompleted;

  /// Task in progress.
  final Color taskInProgress;

  /// Thinking-block marker.
  final Color thinking;

  /// Read and web tools.
  final Color toolRead;

  /// Edit and todo tools.
  final Color toolEdit;

  /// Write tools.
  final Color toolWrite;

  /// Shell and skill tools.
  final Color toolShell;

  /// Search tools.
  final Color toolSearch;

  /// Added file.
  final Color changeAdded;

  /// Modified or unstaged file.
  final Color changeModified;

  /// Deleted file.
  final Color changeDeleted;

  /// Renamed file.
  final Color changeRenamed;

  /// Untracked file.
  final Color changeUntracked;

  /// Commit short hash.
  final Color commitHash;

  /// Syntax: comments.
  final Color syntaxComment;

  /// Syntax: tags.
  final Color syntaxTag;

  /// Syntax: keywords and types.
  final Color syntaxKeyword;

  /// Syntax: strings and code.
  final Color syntaxString;

  /// Syntax: numbers, literals, attributes.
  final Color syntaxNumber;

  /// Syntax: titles.
  final Color syntaxTitle;

  /// Syntax: built-ins, quotes, regexps.
  final Color syntaxBuiltIn;

  /// Syntax: function names and sections.
  final Color syntaxFunction;

  /// Syntax: variables and names.
  final Color syntaxVariable;

  /// Syntax: meta directives.
  final Color syntaxMeta;

  /// The 16 terminal colors: 8 normal, then 8 bright.
  final List<Color> ansi;
  final double radius;
  final AppDensity density;
  final StatusStyle statusStyle;

  /// Space between session rows. Filled cards and comfortable rows are set
  /// apart by a gap; compact quiet rows touch and are split by a divider.
  double get rowGap =>
      statusStyle == StatusStyle.filled || density == AppDensity.comfortable
          ? 8
          : 0;

  @override
  ArgusTokens copyWith({
    Color? canvas,
    Color? surface,
    Color? surfaceRaised,
    Color? line,
    Color? text,
    Color? textSecondary,
    Color? textDim,
    Color? accent,
    Color? onAccent,
    Color? link,
    Color? notice,
    Color? error,
    Color? errorSurface,
    Color? destructive,
    Color? blocked,
    Color? blockedSurface,
    Color? blockedBorder,
    Color? waiting,
    Color? waitingSurface,
    Color? waitingBorder,
    Color? working,
    Color? idle,
    Color? dead,
    Color? diffAdd,
    Color? diffAddSurface,
    Color? diffRemove,
    Color? diffRemoveSurface,
    Color? diffHunk,
    Color? terminalFg,
    Color? terminalBg,
    Color? terminalCursor,
    Color? terminalSelection,
    Color? keycapFill,
    Color? keycapBorder,
    Color? keycapText,
    Color? keycapArmed,
    Color? keycapArmedText,
    Color? scrim,
    Color? success,
    Color? warning,
    Color? alert,
    Color? caution,
    Color? busy,
    Color? workingBadge,
    Color? taskCompleted,
    Color? taskInProgress,
    Color? thinking,
    Color? toolRead,
    Color? toolEdit,
    Color? toolWrite,
    Color? toolShell,
    Color? toolSearch,
    Color? changeAdded,
    Color? changeModified,
    Color? changeDeleted,
    Color? changeRenamed,
    Color? changeUntracked,
    Color? commitHash,
    Color? syntaxComment,
    Color? syntaxTag,
    Color? syntaxKeyword,
    Color? syntaxString,
    Color? syntaxNumber,
    Color? syntaxTitle,
    Color? syntaxBuiltIn,
    Color? syntaxFunction,
    Color? syntaxVariable,
    Color? syntaxMeta,
    List<Color>? ansi,
    double? radius,
    AppDensity? density,
    StatusStyle? statusStyle,
  }) => ArgusTokens(
    canvas: canvas ?? this.canvas,
    surface: surface ?? this.surface,
    surfaceRaised: surfaceRaised ?? this.surfaceRaised,
    line: line ?? this.line,
    text: text ?? this.text,
    textSecondary: textSecondary ?? this.textSecondary,
    textDim: textDim ?? this.textDim,
    accent: accent ?? this.accent,
    onAccent: onAccent ?? this.onAccent,
    link: link ?? this.link,
    notice: notice ?? this.notice,
    error: error ?? this.error,
    errorSurface: errorSurface ?? this.errorSurface,
    destructive: destructive ?? this.destructive,
    blocked: blocked ?? this.blocked,
    blockedSurface: blockedSurface ?? this.blockedSurface,
    blockedBorder: blockedBorder ?? this.blockedBorder,
    waiting: waiting ?? this.waiting,
    waitingSurface: waitingSurface ?? this.waitingSurface,
    waitingBorder: waitingBorder ?? this.waitingBorder,
    working: working ?? this.working,
    idle: idle ?? this.idle,
    dead: dead ?? this.dead,
    diffAdd: diffAdd ?? this.diffAdd,
    diffAddSurface: diffAddSurface ?? this.diffAddSurface,
    diffRemove: diffRemove ?? this.diffRemove,
    diffRemoveSurface: diffRemoveSurface ?? this.diffRemoveSurface,
    diffHunk: diffHunk ?? this.diffHunk,
    terminalFg: terminalFg ?? this.terminalFg,
    terminalBg: terminalBg ?? this.terminalBg,
    terminalCursor: terminalCursor ?? this.terminalCursor,
    terminalSelection: terminalSelection ?? this.terminalSelection,
    keycapFill: keycapFill ?? this.keycapFill,
    keycapBorder: keycapBorder ?? this.keycapBorder,
    keycapText: keycapText ?? this.keycapText,
    keycapArmed: keycapArmed ?? this.keycapArmed,
    keycapArmedText: keycapArmedText ?? this.keycapArmedText,
    scrim: scrim ?? this.scrim,
    success: success ?? this.success,
    warning: warning ?? this.warning,
    alert: alert ?? this.alert,
    caution: caution ?? this.caution,
    busy: busy ?? this.busy,
    workingBadge: workingBadge ?? this.workingBadge,
    taskCompleted: taskCompleted ?? this.taskCompleted,
    taskInProgress: taskInProgress ?? this.taskInProgress,
    thinking: thinking ?? this.thinking,
    toolRead: toolRead ?? this.toolRead,
    toolEdit: toolEdit ?? this.toolEdit,
    toolWrite: toolWrite ?? this.toolWrite,
    toolShell: toolShell ?? this.toolShell,
    toolSearch: toolSearch ?? this.toolSearch,
    changeAdded: changeAdded ?? this.changeAdded,
    changeModified: changeModified ?? this.changeModified,
    changeDeleted: changeDeleted ?? this.changeDeleted,
    changeRenamed: changeRenamed ?? this.changeRenamed,
    changeUntracked: changeUntracked ?? this.changeUntracked,
    commitHash: commitHash ?? this.commitHash,
    syntaxComment: syntaxComment ?? this.syntaxComment,
    syntaxTag: syntaxTag ?? this.syntaxTag,
    syntaxKeyword: syntaxKeyword ?? this.syntaxKeyword,
    syntaxString: syntaxString ?? this.syntaxString,
    syntaxNumber: syntaxNumber ?? this.syntaxNumber,
    syntaxTitle: syntaxTitle ?? this.syntaxTitle,
    syntaxBuiltIn: syntaxBuiltIn ?? this.syntaxBuiltIn,
    syntaxFunction: syntaxFunction ?? this.syntaxFunction,
    syntaxVariable: syntaxVariable ?? this.syntaxVariable,
    syntaxMeta: syntaxMeta ?? this.syntaxMeta,
    ansi: ansi ?? this.ansi,
    radius: radius ?? this.radius,
    density: density ?? this.density,
    statusStyle: statusStyle ?? this.statusStyle,
  );

  @override
  ArgusTokens lerp(ArgusTokens? other, double t) {
    if (other == null) return this;
    Color c(Color a, Color b) => Color.lerp(a, b, t)!;
    final late = t >= 0.5;
    return ArgusTokens(
      canvas: c(canvas, other.canvas),
      surface: c(surface, other.surface),
      surfaceRaised: c(surfaceRaised, other.surfaceRaised),
      line: c(line, other.line),
      text: c(text, other.text),
      textSecondary: c(textSecondary, other.textSecondary),
      textDim: c(textDim, other.textDim),
      accent: c(accent, other.accent),
      onAccent: c(onAccent, other.onAccent),
      link: c(link, other.link),
      notice: c(notice, other.notice),
      error: c(error, other.error),
      errorSurface: c(errorSurface, other.errorSurface),
      destructive: c(destructive, other.destructive),
      blocked: c(blocked, other.blocked),
      blockedSurface: c(blockedSurface, other.blockedSurface),
      blockedBorder: c(blockedBorder, other.blockedBorder),
      waiting: c(waiting, other.waiting),
      waitingSurface: c(waitingSurface, other.waitingSurface),
      waitingBorder: c(waitingBorder, other.waitingBorder),
      working: c(working, other.working),
      idle: c(idle, other.idle),
      dead: c(dead, other.dead),
      diffAdd: c(diffAdd, other.diffAdd),
      diffAddSurface: c(diffAddSurface, other.diffAddSurface),
      diffRemove: c(diffRemove, other.diffRemove),
      diffRemoveSurface: c(diffRemoveSurface, other.diffRemoveSurface),
      diffHunk: c(diffHunk, other.diffHunk),
      terminalFg: c(terminalFg, other.terminalFg),
      terminalBg: c(terminalBg, other.terminalBg),
      terminalCursor: c(terminalCursor, other.terminalCursor),
      terminalSelection: c(terminalSelection, other.terminalSelection),
      keycapFill: c(keycapFill, other.keycapFill),
      keycapBorder: c(keycapBorder, other.keycapBorder),
      keycapText: c(keycapText, other.keycapText),
      keycapArmed: c(keycapArmed, other.keycapArmed),
      keycapArmedText: c(keycapArmedText, other.keycapArmedText),
      scrim: c(scrim, other.scrim),
      success: c(success, other.success),
      warning: c(warning, other.warning),
      alert: c(alert, other.alert),
      caution: c(caution, other.caution),
      busy: c(busy, other.busy),
      workingBadge: c(workingBadge, other.workingBadge),
      taskCompleted: c(taskCompleted, other.taskCompleted),
      taskInProgress: c(taskInProgress, other.taskInProgress),
      thinking: c(thinking, other.thinking),
      toolRead: c(toolRead, other.toolRead),
      toolEdit: c(toolEdit, other.toolEdit),
      toolWrite: c(toolWrite, other.toolWrite),
      toolShell: c(toolShell, other.toolShell),
      toolSearch: c(toolSearch, other.toolSearch),
      changeAdded: c(changeAdded, other.changeAdded),
      changeModified: c(changeModified, other.changeModified),
      changeDeleted: c(changeDeleted, other.changeDeleted),
      changeRenamed: c(changeRenamed, other.changeRenamed),
      changeUntracked: c(changeUntracked, other.changeUntracked),
      commitHash: c(commitHash, other.commitHash),
      syntaxComment: c(syntaxComment, other.syntaxComment),
      syntaxTag: c(syntaxTag, other.syntaxTag),
      syntaxKeyword: c(syntaxKeyword, other.syntaxKeyword),
      syntaxString: c(syntaxString, other.syntaxString),
      syntaxNumber: c(syntaxNumber, other.syntaxNumber),
      syntaxTitle: c(syntaxTitle, other.syntaxTitle),
      syntaxBuiltIn: c(syntaxBuiltIn, other.syntaxBuiltIn),
      syntaxFunction: c(syntaxFunction, other.syntaxFunction),
      syntaxVariable: c(syntaxVariable, other.syntaxVariable),
      syntaxMeta: c(syntaxMeta, other.syntaxMeta),
      ansi: List.unmodifiable([
        for (var i = 0; i < ansi.length; i++) c(ansi[i], other.ansi[i]),
      ]),
      radius: late ? other.radius : radius,
      density: late ? other.density : density,
      statusStyle: late ? other.statusStyle : statusStyle,
    );
  }
}
