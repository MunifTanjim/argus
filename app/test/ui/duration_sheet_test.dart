import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/ui/duration_sheet.dart';

void main() {
  Future<String?> pick(WidgetTester tester, String? item) async {
    String? result;
    await tester.pumpWidget(MaterialApp(
      home: Builder(
        builder: (context) => TextButton(
          onPressed: () async => result = await pickUntil(
            context,
            indefiniteLabel: 'Until I turn it off',
            indefinite: 'FOREVER',
          ),
          child: const Text('open'),
        ),
      ),
    ));
    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();
    if (item == null) {
      await tester.tapAt(const Offset(10, 10));
    } else {
      await tester.tap(find.text(item));
    }
    await tester.pumpAndSettle();
    return result;
  }

  testWidgets('a duration returns a UTC time ahead of now', (tester) async {
    final before = DateTime.now().toUtc();
    final r = await pick(tester, '1 hour');
    expect(r, endsWith('Z'));
    final d = DateTime.parse(r!).difference(before);
    expect(d.inMinutes, inInclusiveRange(59, 61));
  });

  testWidgets('the last item returns the indefinite value', (tester) async {
    expect(await pick(tester, 'Until I turn it off'), 'FOREVER');
  });

  testWidgets('a dismissed sheet returns null', (tester) async {
    expect(await pick(tester, null), isNull);
    expect(find.text('1 hour'), findsNothing);
  });
}
