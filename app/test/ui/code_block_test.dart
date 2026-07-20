import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/code_block.dart';

void main() {
  test('extractMarkdown anchors a prose line, restoring its markers', () {
    const src =
        '# Title\n\nSome **bold** and a [link](http://x).\n\nTail para.';
    expect(extractMarkdown(src, 'Some bold and a link.'),
        'Some **bold** and a [link](http://x).');
  });

  test('extractMarkdown expands a table hit to the whole table', () {
    const src = 'before\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\nafter';
    // Cells serialize without pipes ("a b"), so the selection still anchors.
    expect(extractMarkdown(src, 'a b'), '| a | b |\n|---|---|\n| 1 | 2 |');
  });

  test('extractMarkdown expands a fenced-code hit to include the fences', () {
    const src = 'p\n\n```dart\nvoid main() {}\n```\n\nq';
    expect(
        extractMarkdown(src, 'void main() {}'), '```dart\nvoid main() {}\n```');
  });

  test('extractMarkdown spans multiple lines from lead to tail', () {
    const src = 'one\ntwo\nthree\nfour';
    expect(extractMarkdown(src, 'two three'), 'two\nthree');
  });

  test('extractMarkdown anchors to the first occurrence, not across duplicates',
      () {
    expect(extractMarkdown('alpha\nbeta\nalpha\ngamma', 'alpha'), 'alpha');
  });

  test('extractMarkdown expands a table data-row hit to the whole table', () {
    const src = 'before\n\n| a | b |\n|---|---|\n| 1 | 2 |\n\nafter';
    expect(extractMarkdown(src, '1 2'), '| a | b |\n|---|---|\n| 1 | 2 |');
  });

  test('extractMarkdown restores markers across every spanned line', () {
    const src = 'A **bold** word.\nAnother *italic* one.';
    expect(extractMarkdown(src, 'A bold word. Another italic one.'), src);
  });

  test('extractMarkdown keeps inline-code markers, anchoring past them', () {
    // Anchors the second line only if `|` inside inline code survives.
    const src = 'First line.\nCall `foo(a|b)` now.';
    expect(extractMarkdown(src, 'First line. Call foo(a|b) now.'), src);
  });

  test('extractMarkdown returns null when unanchorable', () {
    const src = 'hello world';
    expect(extractMarkdown(src, ''), isNull);
    expect(extractMarkdown(src, 'nothing like this at all zzz'), isNull);
    expect(extractMarkdown(src, null), isNull);
  });

  test('safeFence is at least 3 and longer than any inner run', () {
    expect(safeFence('plain text'), '```');
    expect(safeFence('has ``` fence'), '````');
    expect(safeFence('nested ```` four'), '`````');
  });

  testWidgets('codeBlock renders the body text', (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(body: codeBlock('hello world', lang: 'bash'))));
    expect(find.textContaining('hello world'), findsOneWidget);
  });

  testWidgets('empty body shows an (empty) marker', (tester) async {
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: codeBlock(''))));
    expect(find.text('(empty)'), findsOneWidget);
  });

  testWidgets('header shows the language label', (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(body: codeBlock('echo hi', lang: 'sh'))));
    expect(find.text('bash'), findsOneWidget); // sh aliases to bash
  });

  testWidgets('copy button copies the body and shows a snackbar',
      (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(body: codeBlock('the code', lang: 'bash'))));
    await tester.tap(find.byIcon(Icons.copy));
    await tester.pump();
    expect(find.text('Copied'), findsOneWidget);
  });

  testWidgets('wrap toggle switches horizontal scrolling on and off',
      (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(body: codeBlock('a very long line', lang: 'bash'))));
    // Default: no wrap → the body scrolls horizontally.
    expect(find.byType(SingleChildScrollView), findsOneWidget);
    await tester.tap(find.byIcon(Icons.wrap_text));
    await tester.pump();
    expect(find.byType(SingleChildScrollView), findsNothing);
  });

  testWidgets('wrap: true starts wrapped (no horizontal scroll)',
      (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(
            body: codeBlock('a very long line', lang: 'bash', wrap: true))));
    expect(find.byType(SingleChildScrollView), findsNothing);
    await tester.tap(find.byIcon(Icons.wrap_text)); // still toggleable
    await tester.pump();
    expect(find.byType(SingleChildScrollView), findsOneWidget);
  });

  testWidgets('highlight toggle switches to plain and back', (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(body: codeBlock('final x = 1;', lang: 'dart'))));
    expect(find.byTooltip('Disable highlight'), findsOneWidget);
    await tester.tap(find.byIcon(Icons.format_color_reset));
    await tester.pump();
    expect(find.byTooltip('Enable highlight'), findsOneWidget);
    expect(find.textContaining('final x = 1;'), findsOneWidget); // text kept
  });

  testWidgets('line-number toggle shows a gutter numbered per line',
      (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(body: codeBlock('one\ntwo\nthree', lang: 'text'))));
    expect(find.text('1'), findsNothing); // off by default
    await tester.tap(find.byIcon(Icons.format_list_numbered));
    await tester.pump();
    expect(find.text('1'), findsOneWidget);
    expect(find.text('2'), findsOneWidget);
    expect(find.text('3'), findsOneWidget);
  });

  testWidgets('lineNumberToggle: false hides the gutter button',
      (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(
            body: codeBlock('one\ntwo', lang: 'text',
                lineNumberToggle: false))));
    expect(find.byIcon(Icons.format_list_numbered), findsNothing);
    expect(find.byIcon(Icons.wrap_text), findsOneWidget); // others remain
  });

  testWidgets('copyToClipboard shows a Copied snackbar', (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(
            body: Builder(
                builder: (ctx) => TextButton(
                      onPressed: () => copyToClipboard(ctx, 'x'),
                      child: const Text('tap'),
                    )))));
    await tester.tap(find.text('tap'));
    await tester.pump();
    expect(find.text('Copied'), findsOneWidget);
  });

  testWidgets('standalone codeBlock is wrapped in a SelectionArea',
      (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(body: codeBlock('selectable code', lang: 'bash'))));
    expect(find.byType(SelectionArea), findsOneWidget);
  });

  testWidgets('appMarkdown wraps prose in a SelectionArea', (tester) async {
    await tester.pumpWidget(
        MaterialApp(home: Scaffold(body: appMarkdown('some **prose** here'))));
    expect(find.byType(SelectionArea), findsOneWidget);
  });

  testWidgets('fenced code inside markdown does not nest a SelectionArea',
      (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(
            body: appMarkdown('intro text\n\n```bash\necho hi\n```\n'))));
    // Exactly one: the outer appMarkdown area. The nested code block must use
    // selectable:false, so it adds no SelectionArea of its own.
    expect(find.byType(SelectionArea), findsOneWidget);
  });

  testWidgets('tapping a markdown link opens the actions sheet', (tester) async {
    await tester.pumpWidget(MaterialApp(
        home: Scaffold(
            body: appMarkdown('see [docs](https://argus.muniftanjim.dev)'))));
    await tester.tap(find.text('docs'));
    await tester.pumpAndSettle();

    expect(find.text('https://argus.muniftanjim.dev'), findsOneWidget);
    expect(find.text('Open link'), findsOneWidget);
    expect(find.text('Copy link'), findsOneWidget);
  });

  testWidgets('Open link in the sheet calls openExternalUrl', (tester) async {
    final tapped = <String>[];
    final original = openExternalUrl;
    openExternalUrl = (url) async => tapped.add(url);
    addTearDown(() => openExternalUrl = original);

    await tester.pumpWidget(MaterialApp(
        home: Scaffold(
            body: appMarkdown('see [docs](https://argus.muniftanjim.dev)'))));
    await tester.tap(find.text('docs'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Open link'));
    await tester.pumpAndSettle();

    expect(tapped, ['https://argus.muniftanjim.dev']);
  });

  testWidgets('Copy link in the sheet copies the url to the clipboard',
      (tester) async {
    final copied = <String>[];
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        SystemChannels.platform, (call) async {
      if (call.method == 'Clipboard.setData') {
        copied.add((call.arguments as Map)['text'] as String);
      }
      return null;
    });
    addTearDown(() => tester.binding.defaultBinaryMessenger
        .setMockMethodCallHandler(SystemChannels.platform, null));

    await tester.pumpWidget(MaterialApp(
        home: Scaffold(
            body: appMarkdown('see [docs](https://argus.muniftanjim.dev)'))));
    await tester.tap(find.text('docs'));
    await tester.pumpAndSettle();
    await tester.tap(find.text('Copy link'));
    await tester.pumpAndSettle();

    expect(copied, ['https://argus.muniftanjim.dev']);
  });
}
