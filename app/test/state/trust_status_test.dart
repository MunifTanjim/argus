import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/state/device_identity.dart';

TrustSummary _summary({
  bool connected = true,
  bool? isLocked = true,
  bool isAuthorized = false,
  bool isDisabled = false,
  Uint8List? supersededByGenesis,
  bool canAdoptSupersedingRoot = false,
}) =>
    TrustSummary(
      connected: connected,
      isLocked: isLocked,
      isAuthorized: isAuthorized,
      isDisabled: isDisabled,
      tip: null,
      supersededByGenesis: supersededByGenesis,
      canAdoptSupersedingRoot: canAdoptSupersedingRoot,
    );

void main() {
  group('trustStatusOf', () {
    test('disconnected', () {
      expect(trustStatusOf(const TrustSummary.disconnected()), TrustStatus.notConnected);
    });

    test('open network when lock state is unknown', () {
      expect(trustStatusOf(_summary(isLocked: null)), TrustStatus.openNetwork);
    });

    test('authorized when locked and authorized', () {
      expect(trustStatusOf(_summary(isAuthorized: true)), TrustStatus.authorized);
    });

    test('awaiting authorization when locked and not authorized', () {
      expect(trustStatusOf(_summary()), TrustStatus.awaitingAuthorization);
    });

    test('disabled when the root is dead and no successor is offered', () {
      expect(trustStatusOf(_summary(isDisabled: true)), TrustStatus.disabled);
    });

    test('superseded with a live root offers re-pin', () {
      final s = _summary(
        isDisabled: true,
        supersededByGenesis: Uint8List.fromList([1, 2, 3]),
        canAdoptSupersedingRoot: true,
      );
      expect(trustStatusOf(s), TrustStatus.supersededLiveRoot);
    });

    test('superseded without a live root waits', () {
      final s = _summary(
        isDisabled: true,
        supersededByGenesis: Uint8List.fromList([1, 2, 3]),
        canAdoptSupersedingRoot: false,
      );
      expect(trustStatusOf(s), TrustStatus.supersededNoRoot);
    });

    test('supersession outranks the plain disabled headline', () {
      // Both flags set: the user must see "superseded", not the dead-end "disabled".
      final s = _summary(
        isDisabled: true,
        supersededByGenesis: Uint8List.fromList([9]),
        canAdoptSupersedingRoot: true,
      );
      expect(trustStatusOf(s), isNot(TrustStatus.disabled));
    });
  });

  group('trustSignersVerifiable', () {
    test('true for a live root (authorized or awaiting)', () {
      expect(trustSignersVerifiable(TrustStatus.authorized), isTrue);
      expect(trustSignersVerifiable(TrustStatus.awaitingAuthorization), isTrue);
    });

    test('false for a dead root (disabled or superseded)', () {
      expect(trustSignersVerifiable(TrustStatus.disabled), isFalse);
      expect(trustSignersVerifiable(TrustStatus.supersededLiveRoot), isFalse);
      expect(trustSignersVerifiable(TrustStatus.supersededNoRoot), isFalse);
    });
  });
}
