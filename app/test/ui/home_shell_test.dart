import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/ui/home_shell.dart';

void main() {
  testWidgets('switches to Settings tab and shows Disconnect', (tester) async {
    await tester.pumpWidget(ProviderScope(
      overrides: [gatewayProvider.overrideWithValue(null)],
      child: const MaterialApp(home: HomeShell()),
    ));
    await tester.pump();

    expect(find.text('Sessions'), findsWidgets);
    await tester.tap(find.text('Settings'));
    await tester.pumpAndSettle();
    expect(find.text('Disconnect'), findsOneWidget);
  });
}
