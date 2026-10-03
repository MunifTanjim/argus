import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/node_header.dart';

void main() {
  Future<void> pump(WidgetTester tester, Widget w) =>
      tester.pumpWidget(MaterialApp(home: Scaffold(body: w)));

  testWidgets('shows the server icon and the label in uppercase', (tester) async {
    await pump(tester, const NodeHeader(label: 'macbook'));
    expect(find.byIcon(Icons.dns_outlined), findsOneWidget);
    expect(find.text('MACBOOK'), findsOneWidget);
  });

  testWidgets('marks an offline node', (tester) async {
    await pump(tester, const NodeHeader(label: 'macbook', offline: true));
    expect(find.text('MACBOOK (OFFLINE)'), findsOneWidget);
  });

  testWidgets('needs you shows the needs-input diamond', (tester) async {
    await pump(tester, const NeedsYouHeader(label: 'Needs you'));
    expect(find.text('◆'), findsOneWidget);
    expect(find.text('NEEDS YOU'), findsOneWidget);
  });
}
