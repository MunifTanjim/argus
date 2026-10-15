import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/session.dart';

Session _s(Map<String, dynamic> extra) => Session.fromJson({
      'id': 'n:%1',
      'agent': 'claude',
      'status': 'idle',
      'source': 'hooked',
      'tmux': {'pane_id': '%1'},
      ...extra,
    });

void main() {
  test('parses status_since and effort', () {
    final s = _s({
      'status_since': '2026-10-15T09:00:00Z',
      'summary': {'model_name': 'Opus 5.5', 'effort': 'high'},
    });
    expect(s.statusSince, DateTime.utc(2026, 10, 15, 9));
    expect(s.summary!.effort, 'high');
  });

  test('an old node omits both', () {
    final s = _s({'summary': {'model_name': 'Opus 5.5'}});
    expect(s.statusSince, isNull);
    expect(s.summary!.effort, isNull);
  });

  test('promptReady mirrors the node and blocks a starting session', () {
    expect(_s({}).promptReady, isTrue);
    expect(_s({'interaction': {'kind': 'idle'}, 'status': 'awaiting_input'}).promptReady, isTrue);
    expect(_s({'status': 'working'}).promptReady, isFalse);
    expect(_s({'status': 'starting'}).promptReady, isFalse);
    expect(_s({'status': 'awaiting_input', 'interaction': {'kind': 'permission'}}).promptReady, isFalse);
  });
}
