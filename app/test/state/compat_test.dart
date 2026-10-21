import 'package:flutter_test/flutter_test.dart';
import 'package:argus/state/compat.dart';

void main() {
  test('release versions compare by semver', () {
    expect(isSupported('1.1.2', '1.1.2'), isTrue);
    expect(isSupported('1.2.0', '1.1.2'), isTrue);
    expect(isSupported('1.1.1', '1.1.2'), isFalse);
    expect(isSupported('0.1.1', '1.1.2'), isFalse);
  });

  test('git describe builds count as their base release', () {
    expect(isSupported('1.1.2-7-g17d66765', '1.1.2'), isTrue);
    expect(isSupported('1.1.1-7-g17d66765', '1.1.2'), isFalse);
    expect(isSupported('1.1.2-7-g17d66765-dirty', '1.1.2'), isTrue);
    expect(isSupported('1.1.2-dirty', '1.1.2'), isTrue);
    expect(isSupported('1.1.1-dirty', '1.1.2'), isFalse);
  });

  test('a leading v is accepted', () {
    expect(isSupported('v1.1.2', '1.1.2'), isTrue);
  });

  test('pre-releases of the minimum are below it', () {
    expect(isSupported('1.1.2-rc.1', '1.1.2'), isFalse);
  });

  test('unknown versions pass', () {
    expect(isSupported('dev', '1.1.2'), isTrue);
    expect(isSupported('', '1.1.2'), isTrue);
    expect(isSupported('garbage', '1.1.2'), isTrue);
  });

  test('upgrade is available only for a newer release', () {
    expect(upgradeAvailable('0.1.3', '0.1.3'), isFalse);
    expect(upgradeAvailable('0.1.3', '0.1.4'), isTrue);
    expect(upgradeAvailable('0.1.4', '0.1.3'), isFalse);
    expect(upgradeAvailable('0.1.3-14-g0cac2131', '0.1.3'), isFalse);
    expect(upgradeAvailable('0.1.3-14-g0cac2131-dirty', '0.1.3'), isFalse);
    expect(upgradeAvailable('0.1.3-14-g0cac2131', '0.1.4'), isTrue);
    expect(upgradeAvailable('0.1.3', 'v0.1.3'), isFalse);
  });

  test('unparsable versions upgrade when they differ', () {
    expect(upgradeAvailable('dev', '0.1.3'), isTrue);
    expect(upgradeAvailable('0.1.3', ''), isFalse);
  });
}
