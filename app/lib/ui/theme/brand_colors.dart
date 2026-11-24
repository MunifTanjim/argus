import 'package:flutter/material.dart';

import 'tokens.dart';

/// Each agent's brand color. Fixed across palettes.
Color agentBrandColor(String agent) => switch (agent) {
  'claude' => const Color(0xFFfe8019),
  'codex' => const Color(0xFFb8bb26),
  'antigravity' => const Color(0xFF83a598),
  // Monochrome, matching OpenCode's terminal brand.
  'opencode' => const Color(0xFFebdbb2),
  'pi' => const Color(0xFFb294bb),
  _ => const Color(0xFF928374),
};

/// A Claude Code team color by name; [ArgusTokens.accent] for an unknown name.
Color teamBrandColor(String? name, ArgusTokens t) => switch (name) {
  'blue' => const Color(0xFF83a598),
  'green' => const Color(0xFFb8bb26),
  'red' => const Color(0xFFfb4934),
  'yellow' => const Color(0xFFfabd2f),
  'purple' => const Color(0xFFd3869b),
  'cyan' => const Color(0xFF8ec07c),
  'orange' => const Color(0xFFfe8019),
  'pink' => const Color(0xFFf7a1b0),
  _ => t.accent,
};

/// [c] when it reads on the canvas (contrast 3:1 or more), else
/// [ArgusTokens.text]. Brand colors are fixed, so some vanish on light
/// palettes.
Color readableOn(Color c, ArgusTokens t) =>
    contrastRatio(c, t.canvas) < 3.0 ? t.text : c;
