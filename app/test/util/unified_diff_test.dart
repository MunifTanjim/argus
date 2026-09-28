import 'package:argus/util/unified_diff.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('skips file headers and numbers lines from the hunk header', () {
    const diff =
        'diff --git a/x.go b/x.go\n'
        'index 1..2 100644\n'
        '--- a/x.go\n'
        '+++ b/x.go\n'
        '@@ -10,3 +10,4 @@ func main() {\n'
        ' a\n'
        '-b\n'
        '+c\n'
        '+d\n'
        ' e\n';
    final l = parseUnifiedDiff(diff);
    expect(l.map((x) => x.kind), [
      UdKind.hunk,
      UdKind.context,
      UdKind.del,
      UdKind.add,
      UdKind.add,
      UdKind.context,
    ]);
    expect(l.first.text, '@@ -10,3 +10,4 @@ func main() {');
    expect(l[1].text, 'a');
    expect(l[1].newNo, 10);
    expect(l[2].newNo, isNull);
    expect(l[3].newNo, 11);
    expect(l[4].newNo, 12);
    expect(l[5].newNo, 13);
  });

  test('a deleted line that starts with dashes stays a deletion', () {
    const diff =
        '@@ -1,2 +1,1 @@\n'
        '---- heading\n'
        ' keep\n';
    final l = parseUnifiedDiff(diff);
    expect(l[1].kind, UdKind.del);
    expect(l[1].text, '--- heading');
  });

  test('counts may be omitted and a new file starts at 0,0', () {
    final l = parseUnifiedDiff(
      '--- /dev/null\n+++ b/n.txt\n@@ -0,0 +1,2 @@\n+x\n+y\n',
    );
    expect(l.where((x) => x.kind == UdKind.add).map((x) => x.newNo), [1, 2]);
    final one = parseUnifiedDiff('@@ -3 +3 @@\n-a\n+b\n');
    expect(one.last.newNo, 3);
  });

  test('an empty line inside a hunk is an empty context line', () {
    final l = parseUnifiedDiff('@@ -1,3 +1,3 @@\n a\n\n-b\n+c\n');
    expect(l.map((x) => x.kind), [
      UdKind.hunk,
      UdKind.context,
      UdKind.context,
      UdKind.del,
      UdKind.add,
    ]);
    expect(l[2].text, '');
    expect(l[2].newNo, 2);
    expect(l[4].newNo, 3);
  });

  test('marks the line before a no-newline marker', () {
    final l = parseUnifiedDiff(
      '@@ -1 +1 @@\n-a\n\\ No newline at end of file\n+a\n',
    );
    expect(l[1].kind, UdKind.del);
    expect(l[1].noEol, isTrue);
    expect(l[2].noEol, isFalse);
  });

  test('handles several hunks and CRLF', () {
    const diff =
        '@@ -1,1 +1,1 @@\r\n-a\r\n+b\r\n@@ -9,1 +9,1 @@\r\n-c\r\n+d\r\n';
    final l = parseUnifiedDiff(diff);
    expect(l.where((x) => x.kind == UdKind.hunk), hasLength(2));
    expect(l.last.text, 'd');
    expect(l.last.newNo, 9);
  });

  test('an empty diff has no lines', () {
    expect(parseUnifiedDiff(''), isEmpty);
  });
}
