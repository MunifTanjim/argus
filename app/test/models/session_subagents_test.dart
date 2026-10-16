import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/session.dart';

Map<String, dynamic> _json({List<Map<String, dynamic>>? subagents}) => {
  'id': 'n:%1',
  'agent': 'claude',
  'status': 'working',
  'source': 'hooked',
  'tmux': {'pane_id': '%1'},
  'subagents': ?subagents,
};

void main() {
  test('parses running subagents', () {
    final s = Session.fromJson(
      _json(
        subagents: [
          {
            'id': 'a1',
            'type': 'Explore',
            'name': 'scout',
            'desc': 'find it',
            'since': '2026-10-16T09:00:00Z',
          },
          {'id': 'a2'},
        ],
      ),
    );
    expect(s.subagents, hasLength(2));
    final a = s.subagents.first;
    expect(a.id, 'a1');
    expect(a.type, 'Explore');
    expect(a.name, 'scout');
    expect(a.desc, 'find it');
    expect(a.since, DateTime.utc(2026, 10, 16, 9));
    final b = s.subagents.last;
    expect(b.id, 'a2');
    expect(b.type, isNull);
    expect(b.since, isNull);
  });

  test('absent subagents is empty', () {
    expect(Session.fromJson(_json()).subagents, isEmpty);
  });
}
