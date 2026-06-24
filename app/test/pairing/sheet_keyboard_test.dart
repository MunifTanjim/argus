import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/manual_entry_form.dart';

void main() {
  testWidgets('sheet content stays above the keyboard', (tester) async {
    const kbHeight = 300.0;
    tester.view.physicalSize = const Size(400, 800);
    tester.view.devicePixelRatio = 1.0;
    addTearDown(tester.view.reset);

    await tester.pumpWidget(MaterialApp(
      home: Scaffold(
        body: Builder(
          builder: (context) => Center(
            child: TextButton(
              onPressed: () => showModalBottomSheet<void>(
                context: context,
                isScrollControlled: true,
                builder: (_) => ManualEntryForm(onSubmit: (_) {}),
              ),
              child: const Text('open'),
            ),
          ),
        ),
      ),
    ));

    await tester.tap(find.text('open'));
    await tester.pumpAndSettle();

    // Simulate the keyboard opening.
    tester.view.viewInsets = const FakeViewPadding(bottom: kbHeight);
    await tester.pumpAndSettle();

    final screenH = tester.view.physicalSize.height / tester.view.devicePixelRatio;
    final keyboardTop = screenH - kbHeight;

    final connectBottom = tester.getBottomRight(find.byKey(const Key('connect'))).dy;
    expect(connectBottom, lessThanOrEqualTo(keyboardTop),
        reason: 'Connect button bottom ($connectBottom) is under the keyboard top ($keyboardTop)');
  });
}
