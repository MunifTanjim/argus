import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/aggregate.dart';

void main() {
  test('compositeId / splitCompositeId round-trip and split on first colon', () {
    expect(compositeId('A', 's1'), 'A:s1');
    expect(splitCompositeId('A:s1'), ('A', 's1', true));
    expect(splitCompositeId('A:s:1'), ('A', 's:1', true)); // first colon only
    expect(splitCompositeId('plain'), ('', 'plain', false));
  });

  test('withOriginJson composites id, stamps origin, clears offline', () {
    final out = withOriginJson({'id': 's1', 'offline': true, 'x': 1}, 'A', 'A-box');
    expect(out['id'], 'A:s1');
    expect(out['node_id'], 'A');
    expect(out['node_label'], 'A-box');
    expect(out['offline'], false);
    expect(out['x'], 1); // other fields preserved
  });

  test('rewriteSessionId replaces only session_id, preserving other fields', () {
    final out = rewriteSessionId({'session_id': 'A:s1', 'k': 2}, 's1');
    expect(out['session_id'], 's1');
    expect(out['k'], 2);
    expect(rewriteSessionId(null, 's1'), {'session_id': 's1'});
  });

  test('stringField reads a string field or null', () {
    expect(stringField({'node_id': 'A'}, 'node_id'), 'A');
    expect(stringField({'node_id': 5}, 'node_id'), isNull);
    expect(stringField(null, 'node_id'), isNull);
  });

  test('method sets contain the expected members', () {
    expect(sessionAddressed.contains('sessions.input'), isTrue);
    expect(sessionAddressed.contains('transcript.subscribe'), isTrue);
    expect(sessionAddressed.contains('terminal.open'), isTrue);
    expect(sessionAddressed.contains('sessions.focus'), isTrue);
    expect(nodeAddressed.contains('sessions.spawn'), isTrue);
    expect(nodeAddressed.contains('sessions.historySessions'), isTrue);
    expect(terminalHandleAddressed.contains('terminal.input'), isTrue);
    expect(compositeResultMethods, {'sessions.spawn', 'sessions.resume'});
  });
}
