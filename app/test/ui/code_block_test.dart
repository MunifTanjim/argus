import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/code_block.dart';

void main() {
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
}
