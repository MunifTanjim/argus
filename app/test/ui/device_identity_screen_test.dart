import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/data/trust_chain_store.dart';
import 'package:argus/e2e/e2e.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/device_identity.dart';
import 'package:argus/state/gateway.dart';
import 'package:argus/ui/device_identity_screen.dart';

// In-memory SecureKv that records whether delete() was ever called.
class _TrackingKv implements SecureKv {
  final _m = <String, String>{};
  bool deleted = false;

  @override
  Future<String?> read(String key) async => _m[key];
  @override
  Future<void> write(String key, String value) async => _m[key] = value;
  @override
  Future<void> delete(String key) async {
    deleted = true;
    _m.remove(key);
  }
}

// Fixed 32-byte keypair for deterministic tests.
final _fixedPriv = Uint8List(32)..fillRange(0, 32, 1);
final _fixedPub = Uint8List(32)..fillRange(0, 32, 2);
final _fixedKeyPair = KeyPair(_fixedPriv, _fixedPub);
final _fixedPubB64 = base64.encode(_fixedPub);

// Fixed 32-byte signer pubkey for deterministic verify-section tests.
final _fixedSigner = Uint8List(32)..fillRange(0, 32, 0xAB);

Widget _app(TrustSummary summary, TrustChainStore chainStore) {
  return ProviderScope(
    overrides: [
      deviceIdentityProvider.overrideWith((_) async => _fixedKeyPair),
      trustSummaryProvider.overrideWith((_) => summary),
      trustChainStoreProvider.overrideWithValue(chainStore),
      gatewayProvider.overrideWithValue(null),
    ],
    child: const MaterialApp(home: DeviceIdentityScreen()),
  );
}

void main() {
  testWidgets('status card: open network (connected, isLocked null)',
      (tester) async {
    final store = TrustChainStore(_TrackingKv());
    const summary = TrustSummary(
      connected: true,
      isLocked: null,
      isAuthorized: false,
      isDisabled: false,
      head: null,
    );
    await tester.pumpWidget(_app(summary, store));
    await tester.pumpAndSettle();
    expect(find.text('Open network'), findsOneWidget);
  });

  testWidgets('status card: authorized (locked + authorized)', (tester) async {
    final store = TrustChainStore(_TrackingKv());
    const summary = TrustSummary(
      connected: true,
      isLocked: true,
      isAuthorized: true,
      isDisabled: false,
      head: null,
    );
    await tester.pumpWidget(_app(summary, store));
    await tester.pumpAndSettle();
    expect(find.text('Authorized'), findsOneWidget);
  });

  testWidgets('status card: awaiting authorization (locked + !authorized)',
      (tester) async {
    final store = TrustChainStore(_TrackingKv());
    const summary = TrustSummary(
      connected: true,
      isLocked: true,
      isAuthorized: false,
      isDisabled: false,
      head: null,
    );
    await tester.pumpWidget(_app(summary, store));
    await tester.pumpAndSettle();
    expect(find.text('Awaiting authorization'), findsOneWidget);
  });

  testWidgets('status card: not connected (disconnected)', (tester) async {
    final store = TrustChainStore(_TrackingKv());
    const summary = TrustSummary.disconnected();
    await tester.pumpWidget(_app(summary, store));
    await tester.pumpAndSettle();
    expect(find.text('Not connected'), findsOneWidget);
  });

  testWidgets('status card: disabled (isDisabled true)', (tester) async {
    final store = TrustChainStore(_TrackingKv());
    const summary = TrustSummary(
      connected: true,
      isLocked: true,
      isAuthorized: false,
      isDisabled: true,
      head: null,
    );
    await tester.pumpWidget(_app(summary, store));
    await tester.pumpAndSettle();
    expect(find.text('Disabled'), findsOneWidget);
  });

  testWidgets(
      'enroll section shows device key and argus lock sign command '
      '(initially expanded when awaiting)', (tester) async {
    final store = TrustChainStore(_TrackingKv());
    const summary = TrustSummary(
      connected: true,
      isLocked: true,
      isAuthorized: false,
      isDisabled: false,
      head: null,
    );
    await tester.pumpWidget(_app(summary, store));
    await tester.pumpAndSettle();

    // Enroll tile is initially expanded when awaiting — content visible immediately.
    expect(find.textContaining(_fixedPubB64), findsWidgets);
    expect(
        find.textContaining('argus lock sign $_fixedPubB64'), findsOneWidget);
  });

  testWidgets('verify section shows signer-set fingerprint words and signer pubkeys',
      (tester) async {
    final store = TrustChainStore(_TrackingKv());
    final summary = TrustSummary(
      connected: true,
      isLocked: true,
      isAuthorized: true,
      isDisabled: false,
      head: null,
      signers: [_fixedSigner],
    );
    await tester.pumpWidget(_app(summary, store));
    await tester.pumpAndSettle();

    // Expand the Verify trust tile.
    await tester.tap(find.text('Verify trust'));
    await tester.pumpAndSettle();

    // Verify fingerprint words are shown.
    final words = signerSetFingerprintWords([_fixedSigner]);
    for (final w in words) {
      expect(find.text(w), findsOneWidget);
    }

    // The signer pubkey base64 should appear.
    expect(find.textContaining(base64.encode(_fixedSigner)), findsOneWidget);
  });

  testWidgets('reset trust anchor confirm calls store.clear()', (tester) async {
    final kv = _TrackingKv();
    final store = TrustChainStore(kv);
    const summary = TrustSummary(
      connected: true,
      isLocked: true,
      isAuthorized: true,
      isDisabled: false,
      head: null,
    );
    await tester.pumpWidget(_app(summary, store));
    await tester.pumpAndSettle();

    // Expand Advanced tile.
    await tester.tap(find.text('Advanced'));
    await tester.pumpAndSettle();

    // Tap the reset button.
    await tester.tap(find.text('Reset trust anchor'));
    await tester.pumpAndSettle();

    // Confirm dialog is shown; clear() not yet called.
    expect(kv.deleted, isFalse);

    // Confirm.
    await tester.tap(find.text('Reset'));
    await tester.pumpAndSettle();

    expect(kv.deleted, isTrue);
  });
}
