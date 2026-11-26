import 'package:argus/ui/details_header.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  Future<List<String>> tapRow(WidgetTester tester, DetailsFieldRow row) async {
    final copied = <String>[];
    tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
      SystemChannels.platform,
      (call) async {
        if (call.method == 'Clipboard.setData') {
          copied.add((call.arguments as Map)['text'] as String);
        }
        return null;
      },
    );
    addTearDown(
      () => tester.binding.defaultBinaryMessenger.setMockMethodCallHandler(
        SystemChannels.platform,
        null,
      ),
    );
    await tester.pumpWidget(MaterialApp(home: Scaffold(body: row)));
    await tester.tap(find.text(row.value));
    await tester.pump();
    await tester.pumpAndSettle(const Duration(seconds: 2));
    return copied;
  }

  testWidgets('a field row copies its shown value', (tester) async {
    final copied = await tapRow(
      tester,
      const DetailsFieldRow(label: 'dir', value: '/src/argus'),
    );
    expect(copied, ['/src/argus']);
  });

  testWidgets('a field row with a copy value copies that value', (
    tester,
  ) async {
    final copied = await tapRow(
      tester,
      const DetailsFieldRow(
        label: 'head',
        value: '3f9c2a1',
        copyValue: '3f9c2a1e0b7d',
      ),
    );
    expect(copied, ['3f9c2a1e0b7d']);
  });
}
