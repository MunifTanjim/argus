import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/manual_entry_form.dart';
import 'package:argus/pairing/pairing_uri.dart';

void main() {
  testWidgets('submits trimmed url and token', (tester) async {
    GatewayCredentials? got;
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(body: ManualEntryForm(onSubmit: (c) => got = c)),
    ));

    await tester.enterText(find.byKey(const Key('url')), ' wss://h/client ');
    await tester.enterText(find.byKey(const Key('token')), ' tok ');
    await tester.tap(find.byKey(const Key('connect')));
    await tester.pump();

    expect(got!.url, 'wss://h/client');
    expect(got!.token, 'tok');
  });

  testWidgets('does not submit when a field is empty', (tester) async {
    var called = false;
    await tester.pumpWidget(MaterialApp(
      home: Scaffold(body: ManualEntryForm(onSubmit: (_) => called = true)),
    ));
    await tester.tap(find.byKey(const Key('connect')));
    await tester.pump();
    expect(called, isFalse);
  });
}
