import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/session.dart';
import 'package:argus/ui/status_style.dart';
import 'package:argus/ui/theme.dart';

final _t = ArgusTokens.fallback;

Session _s(String status, [String? kind]) => Session.fromJson({
      'id': 'n:%1',
      'agent': 'claude',
      'status': status,
      'source': 'discovered',
      'tmux': {'server': 'default', 'pane_id': '%1', 'session_name': 's', 'window_index': 0, 'current_path': '/'},
      if (kind != null) 'interaction': {'kind': kind},
    });

void main() {
  test('blocked: awaiting on a permission, question or plan', () {
    for (final k in ['permission', 'question', 'plan']) {
      expect(_s('awaiting_input', k).blocked, isTrue, reason: k);
      expect(sessionColor(_s('awaiting_input', k), _t), _t.blocked, reason: k);
    }
  });

  test('not blocked: finished turn, no interaction, or not awaiting', () {
    expect(_s('awaiting_input', 'idle').blocked, isFalse);
    expect(_s('awaiting_input').blocked, isFalse);
    expect(_s('working', 'permission').blocked, isFalse);
    expect(sessionColor(_s('awaiting_input', 'idle'), _t), _t.waiting);
    expect(sessionColor(_s('awaiting_input'), _t), _t.waiting);
    expect(sessionColor(_s('working'), _t), statusColor(_s('working').status, _t));
  });

  test('interactionPending ignores the idle kind', () {
    expect(_s('awaiting_input', 'permission').interactionPending, isTrue);
    expect(_s('awaiting_input', 'idle').interactionPending, isFalse);
    expect(_s('awaiting_input').interactionPending, isFalse);
  });
}
