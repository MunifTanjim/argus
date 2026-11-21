import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/code_block.dart';
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
    expect(stackedLabel('**Name**', 1), 'Name');
  });

  const cell = 'a long cell value here';
  String table(int cols, int rows) {
    final header = '| ${List.generate(cols, (i) => 'h${i + 1}').join(' | ')} |';
    final delim = '|${'---|' * cols}';
    final body = List.generate(
        rows, (_) => '| ${List.filled(cols, cell).join(' | ')} |');
    return [header, delim, ...body].join('\n');
  }

  Future<void> pumpAt(WidgetTester tester, String md, double width) =>
      tester.pumpWidget(MaterialApp(
        home: Scaffold(
          body: Align(
            alignment: Alignment.topLeft,
            child: SizedBox(width: width, child: appMarkdown(md)),
          ),
        ),
      ));

  testWidgets('markdownTable shows a grid when the table fits', (tester) async {
    await pumpAt(tester, table(4, 1), 800);
    expect(find.byType(Table), findsOneWidget);
    expect(find.byType(Divider), findsNothing);
  });

  testWidgets('markdownTable stacks records when the table is too wide',
      (tester) async {
    await pumpAt(tester, table(4, 2), 300);
    expect(find.byType(Table), findsNothing);
    expect(find.byType(Divider), findsNWidgets(3));
    expect(find.textContaining('h1:', findRichText: true), findsNWidgets(2));
  });

  testWidgets('markdownTable keeps a single column as a grid', (tester) async {
    await pumpAt(tester, table(1, 1), 200);
    expect(find.byType(Table), findsOneWidget);
  });

  testWidgets('markdownTable wraps a long stacked label', (tester) async {
    final md = table(3, 2).replaceFirst(
        'h1', 'A very long header describing the column contents');
    await pumpAt(tester, md, 300);
    expect(tester.takeException(), isNull);
    expect(find.byType(Divider), findsNWidgets(3));
  });

  testWidgets('markdownTable drops cells beyond the header', (tester) async {
    final md = [
      '| h1 | h2 |',
      '|---|---|',
      '| $cell | $cell | extra |',
      '| $cell | $cell |',
    ].join('\n');
    await pumpAt(tester, md, 300);
    expect(find.byType(Divider), findsNWidgets(3));
    expect(find.textContaining('extra', findRichText: true), findsNothing);
    expect(find.textContaining('Column', findRichText: true), findsNothing);
  });

  testWidgets('markdownTable renders header markup in stacked labels',
      (tester) async {
    final md = table(3, 2).replaceFirst('h1', '**Name**');
    await pumpAt(tester, md, 300);
    expect(find.textContaining('Name:', findRichText: true), findsNWidgets(2));
    expect(find.textContaining('**', findRichText: true), findsNothing);
  });
}
