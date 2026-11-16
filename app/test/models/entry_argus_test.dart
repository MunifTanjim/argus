import 'package:argus/models/entry.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('a user entry parses its argus messages', () {
    final e = Entry.fromJson({
      'id': 'u',
      'kind': 'user',
      'text': '<argus-message raw',
      'argusMessages': [
        {
          'id': 'm-1',
          'kind': 'reply',
          'from': 'worker-api',
          'session': 'home:argus:%5',
          'replyTo': 'm-0',
          'body': 'All tests pass.',
        },
        {
          'id': 'm-2',
          'kind': 'interaction',
          'from': 'argus',
          'child': 'worker-db',
          'childSession': 'home:argus:%7',
          'body': 'Tool: Bash',
        },
      ],
    });
    expect(e.argusMessages, hasLength(2));
    final r = e.argusMessages[0];
    expect(
      [r.id, r.kind, r.from, r.session, r.replyTo, r.body],
      ['m-1', 'reply', 'worker-api', 'home:argus:%5', 'm-0', 'All tests pass.'],
    );
    final n = e.argusMessages[1];
    expect([n.child, n.childSession], ['worker-db', 'home:argus:%7']);
    expect(n.replyTo, isNull);
  });

  test('a user entry without the key has no argus messages', () {
    final e = Entry.fromJson({'id': 'u', 'kind': 'user', 'text': 'hi'});
    expect(e.argusMessages, isEmpty);
  });
}
