import 'package:flutter/material.dart';

import '../models/enums.dart';
import '../models/session.dart';
import 'theme.dart';

String statusGlyph(SessionStatus s) {
  switch (s) {
    case SessionStatus.working:
      return '●';
    case SessionStatus.awaitingInput:
      return '◆';
    case SessionStatus.idle:
    case SessionStatus.discovered:
      return '○';
    case SessionStatus.starting:
      return '◌';
    case SessionStatus.dead:
      return '×';
    case SessionStatus.unknown:
      return '·';
  }
}

Color statusColor(SessionStatus s, ArgusTokens t) {
  switch (s) {
    case SessionStatus.working:
      return t.working;
    case SessionStatus.awaitingInput:
      return t.waiting;
    case SessionStatus.dead:
      return t.dead;
    case SessionStatus.idle:
    case SessionStatus.discovered:
    case SessionStatus.starting:
    case SessionStatus.unknown:
      return t.idle;
  }
}

/// [statusColor], with blocked sessions set apart.
Color sessionColor(Session s, ArgusTokens t) =>
    s.blocked ? t.blocked : statusColor(s.status, t);
