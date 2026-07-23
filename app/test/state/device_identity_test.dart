import 'package:flutter_test/flutter_test.dart';
import 'package:argus/state/device_identity.dart';

void main() {
  test('TrustSummary carries the status fields', () {
    const s = TrustSummary(connected: true, isLocked: true, isAuthorized: false, isDisabled: false, tip: null);
    expect(s.connected, isTrue);
    expect(s.isLocked, isTrue);
    expect(s.isAuthorized, isFalse);
  });

  test('disconnected summary has connected=false and isLocked=null', () {
    const s = TrustSummary.disconnected();
    expect(s.connected, isFalse);
    expect(s.isLocked, isNull);
  });
}
