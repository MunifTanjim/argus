import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/changes.dart';

void main() {
  test('CommitList.fromJson parses commits and unpushed flag', () {
    final list = CommitList.fromJson({
      'unpushed': true,
      'commits': [
        {
          'sha': 'abc123',
          'short': 'abc123',
          'subject': 'do a thing',
          'author': 'Munif',
          'unix_sec': 1700000000,
        },
      ],
    });
    expect(list.unpushed, isTrue);
    expect(list.commits, hasLength(1));
    expect(list.commits.first.subject, 'do a thing');
    expect(list.commits.first.unixSec, 1700000000);
  });

  test('CommitList.fromJson tolerates missing fields', () {
    final list = CommitList.fromJson({'commits': []});
    expect(list.unpushed, isFalse);
    expect(list.commits, isEmpty);
  });
}
