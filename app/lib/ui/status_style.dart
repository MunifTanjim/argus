import 'package:flutter/material.dart';

import '../models/enums.dart';

String statusGlyph(SessionStatus s) {
  switch (s) {
    case SessionStatus.working:
      return '●';
    case SessionStatus.awaitingInput:
      return '◆';
    case SessionStatus.idle:
    case SessionStatus.discovered:
      return '○';
    case SessionStatus.dead:
      return '×';
    case SessionStatus.unknown:
      return '·';
  }
}

Color statusColor(SessionStatus s) {
  switch (s) {
    case SessionStatus.working:
      return const Color(0xFFb8bb26);
    case SessionStatus.awaitingInput:
      return const Color(0xFFfabd2f);
    case SessionStatus.dead:
      return const Color(0xFFfb4934);
    case SessionStatus.idle:
    case SessionStatus.discovered:
    case SessionStatus.unknown:
      return const Color(0xFF928374);
  }
}

