import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/session.dart';

Map<String, dynamic> _json({List<Map<String, dynamic>>? shells}) => {
  'id': 'n:%1',
  'agent': 'claude',
  'status': 'working',
  'source': 'hooked',
  'tmux': {'pane_id': '%1'},
  'shells': ?shells,
};

void main() {
  test('parses running shells', () {
    final s = Session.fromJson(
      _json(
        shells: [
          {
            'id': 'b1',
            'tool_use_id': 't1',
            'command': 'make test',
            'desc': 'run tests',
            'since': '2026-10-16T09:00:00Z',
          },
          {'id': 'b2'},
        ],
      ),
    );
    expect(s.shells, hasLength(2));
    final a = s.shells.first;
    expect(a.id, 'b1');
    expect(a.toolUseId, 't1');
    expect(a.command, 'make test');
    expect(a.desc, 'run tests');
    expect(a.since, DateTime.utc(2026, 10, 16, 9));
    final b = s.shells.last;
    expect(b.id, 'b2');
    expect(b.toolUseId, isNull);
    expect(b.command, isNull);
    expect(b.since, isNull);
  });

  test('no shells key means none run', () {
    expect(Session.fromJson(_json()).shells, isEmpty);
  });
}
