import 'package:flutter/material.dart';

import 'theme/build_theme.dart' show monoFamily;

export 'theme/brand_colors.dart';
export 'theme/build_theme.dart';
export 'theme/context.dart';
export 'theme/palette.dart';
export 'theme/palettes.dart';
export 'theme/tokens.dart';

const mono = TextStyle(fontFamily: monoFamily, fontSize: 12);

/// Code, diff, and tool output text.
const codeMono = TextStyle(fontFamily: monoFamily, fontSize: 12, height: 1.35);
