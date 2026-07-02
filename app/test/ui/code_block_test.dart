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

  testWidgets('empty body renders nothing', (tester) async {
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: codeBlock(''))));
    expect(find.byType(SizedBox), findsWidgets);
  });

  testWidgets('CopyOnLongPress shows a Copied snackbar', (tester) async {
    await tester.pumpWidget(const MaterialApp(
        home: Scaffold(
            body: Center(
                child: CopyOnLongPress(text: 'x', child: Text('tap'))))));
    await tester.longPress(find.text('tap'));
    await tester.pump();
    expect(find.text('Copied'), findsOneWidget);
  });
}
