import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/enums.dart';
import 'package:argus/models/session.dart';
import 'package:argus/ui/status_style.dart';
import 'package:argus/ui/theme.dart';

void main() {
  test('glyphs by status', () {
    expect(statusGlyph(SessionStatus.working), '●');
    expect(statusGlyph(SessionStatus.awaitingInput), '◆');
    expect(statusGlyph(SessionStatus.idle), '○');
    expect(statusGlyph(SessionStatus.dead), '×');
    expect(statusGlyph(SessionStatus.unknown), '·');
  });

  test('colors by status', () {
    final t = ArgusTokens.fallback;
    expect(statusColor(SessionStatus.working, t), t.working);
    expect(statusColor(SessionStatus.awaitingInput, t), t.waiting);
    expect(statusColor(SessionStatus.idle, t), t.idle);
    expect(statusColor(SessionStatus.dead, t), t.dead);
  });

  test('statusColor reads the given tokens', () {
    final t = ArgusTokens.fallback.copyWith(
      working: const Color(0xFF010203),
      dead: const Color(0xFF040506),
    );
    expect(statusColor(SessionStatus.working, t), const Color(0xFF010203));
    expect(statusColor(SessionStatus.dead, t), const Color(0xFF040506));
  });

  test('blocked sessions use the blocked color', () {
    final t = ArgusTokens.fallback;
    final blocked = Session.fromJson({
      'id': 'n:%1',
      'agent': 'claude',
      'status': 'awaiting_input',
      'source': 'discovered',
      'tmux': {
        'server': 'default',
        'pane_id': '%1',
        'session_name': 's',
        'window_index': 0,
        'current_path': '/',
      },
      'interaction': {'kind': 'permission'},
    });
    expect(sessionColor(blocked, t), t.blocked);
  });
}
