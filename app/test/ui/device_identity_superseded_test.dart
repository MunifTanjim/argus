import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:argus/state/device_identity.dart';
import 'package:argus/ui/device_identity_screen.dart';

Widget _app(TrustSummary summary) => ProviderScope(
      overrides: [trustSummaryProvider.overrideWithValue(summary)],
      child: const MaterialApp(home: DeviceIdentityScreen()),
    );

TrustSummary _superseded({required bool canAdopt}) => TrustSummary(
      connected: true,
      isLocked: true,
      isAuthorized: false,
      isDisabled: true,
      tip: null,
      supersededByGenesis: Uint8List.fromList(List<int>.filled(32, 7)),
      canAdoptSupersedingRoot: canAdopt,
    );

void main() {
  testWidgets('a live-successor supersession shows the re-establish action',
      (tester) async {
    await tester.pumpWidget(_app(_superseded(canAdopt: true)));
    await tester.pump();

    expect(find.text('Trust root superseded'), findsOneWidget);
    expect(find.text('New root fingerprint'), findsOneWidget);
    expect(find.text('Re-establish trust'), findsOneWidget);

    await tester.tap(find.text('Re-establish trust'));
    await tester.pumpAndSettle();
    expect(find.text('Re-establish trust?'), findsOneWidget); // confirm dialog
  });

  testWidgets('a superseded root without a live successor shows no action',
      (tester) async {
    await tester.pumpWidget(_app(_superseded(canAdopt: false)));
    await tester.pump();

    expect(find.text('Trust root superseded'), findsOneWidget);
    expect(find.text('Re-establish trust'), findsNothing);
  });

  testWidgets('a plain disabled network is not shown as superseded',
      (tester) async {
    await tester.pumpWidget(_app(const TrustSummary(
      connected: true,
      isLocked: true,
      isAuthorized: false,
      isDisabled: true,
      tip: null,
    )));
    await tester.pump();

    expect(find.text('Disabled'), findsOneWidget);
    expect(find.text('Trust root superseded'), findsNothing);
    expect(find.text('Re-establish trust'), findsNothing);
  });

  testWidgets('Verify trust is hidden while the root is dead', (tester) async {
    final signers = [Uint8List.fromList(List<int>.filled(32, 1))];
    await tester.pumpWidget(_app(TrustSummary(
      connected: true,
      isLocked: true,
      isAuthorized: false,
      isDisabled: true,
      tip: null,
      signers: signers,
      supersededByGenesis: Uint8List.fromList(List<int>.filled(32, 7)),
      canAdoptSupersedingRoot: true,
    )));
    await tester.pump();

    expect(find.text('Verify trust'), findsNothing);
  });

  testWidgets('Verify trust is shown for a live root', (tester) async {
    final signers = [Uint8List.fromList(List<int>.filled(32, 1))];
    await tester.pumpWidget(_app(TrustSummary(
      connected: true,
      isLocked: true,
      isAuthorized: true,
      isDisabled: false,
      tip: null,
      signers: signers,
    )));
    await tester.pump();

    expect(find.text('Verify trust'), findsOneWidget);
  });
}
