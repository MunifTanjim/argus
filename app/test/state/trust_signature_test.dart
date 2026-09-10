import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/state/device_identity.dart';

TrustSummary _s({
  bool connected = true,
  bool? isLocked = true,
  bool isAuthorized = false,
  bool isDisabled = false,
  Uint8List? tip,
  Uint8List? supersededByGenesis,
  bool canAdoptSupersedingRoot = false,
}) =>
    TrustSummary(
      connected: connected,
      isLocked: isLocked,
      isAuthorized: isAuthorized,
      isDisabled: isDisabled,
      tip: tip,
      supersededByGenesis: supersededByGenesis,
      canAdoptSupersedingRoot: canAdoptSupersedingRoot,
    );

void main() {
  group('trustSignatureOf', () {
    test('is stable for the same trust state', () {
      expect(trustSignatureOf(_s()), equals(trustSignatureOf(_s())));
    });

    test('changes when the disabled flag flips', () {
      expect(trustSignatureOf(_s(isDisabled: false)),
          isNot(trustSignatureOf(_s(isDisabled: true))));
    });

    test('changes when a superseding root appears', () {
      expect(
        trustSignatureOf(_s()),
        isNot(trustSignatureOf(_s(
          isDisabled: true,
          supersededByGenesis: Uint8List.fromList([1, 2, 3]),
          canAdoptSupersedingRoot: true,
        ))),
      );
    });

    test('changes when the adoptable flag flips', () {
      final g = Uint8List.fromList([1, 2, 3]);
      expect(
        trustSignatureOf(_s(isDisabled: true, supersededByGenesis: g, canAdoptSupersedingRoot: false)),
        isNot(trustSignatureOf(_s(isDisabled: true, supersededByGenesis: g, canAdoptSupersedingRoot: true))),
      );
    });

    test('changes when the tip advances', () {
      expect(trustSignatureOf(_s(tip: Uint8List.fromList([1]))),
          isNot(trustSignatureOf(_s(tip: Uint8List.fromList([2])))));
    });

    test('changes when authorization is granted', () {
      expect(trustSignatureOf(_s(isAuthorized: false)),
          isNot(trustSignatureOf(_s(isAuthorized: true))));
    });

    test('changes on connect and disconnect', () {
      expect(trustSignatureOf(const TrustSummary.disconnected()),
          isNot(trustSignatureOf(_s())));
    });
  });
}
