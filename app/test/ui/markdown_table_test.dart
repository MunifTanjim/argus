import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/markdown_table.dart';

void main() {
  test('stripInlineMarkdown removes inline markup', () {
    expect(stripInlineMarkdown('`x`'), 'x');
    expect(stripInlineMarkdown('**b** and __b__'), 'b and b');
    expect(stripInlineMarkdown('*i* _i_ ~~s~~'), 'i i s');
    expect(stripInlineMarkdown('[t](https://a.b/c)'), 't');
    expect(stripInlineMarkdown(r'a \| b'), 'a | b');
    expect(stripInlineMarkdown('snake_case_name'), 'snake_case_name');
  });

  test('tableFitsGrid applies floors and overhead', () {
    // overhead: 16 px padding per column + 1 px per border line
    expect(tableFitsGrid([40, 60, 20], 100, 172), isTrue); // 120 + 52
    expect(tableFitsGrid([40, 60, 20], 100, 171), isFalse);
    expect(tableFitsGrid([300, 300], 100, 235), isTrue); // 200 + 35
    expect(tableFitsGrid([300, 300], 100, 234), isFalse);
    expect(tableFitsGrid([30, 2000], 100, 165), isTrue); // 130 + 35
  });

  test('stackedLabel falls back to column number', () {
    expect(stackedLabel('Name', 1), 'Name');
    expect(stackedLabel('  ', 2), 'Column 2');
  });
}
