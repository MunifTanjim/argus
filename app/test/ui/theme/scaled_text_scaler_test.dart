import 'package:argus/ui/theme/scaled_text_scaler.dart';
import 'package:flutter/widgets.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('multiplies the base scaler and compares by value', () {
    const base = TextScaler.linear(2);
    const s = ScaledTextScaler(base, 1.15);
    expect(s.scale(14), closeTo(14 * 2 * 1.15, 1e-9));
    // ignore: deprecated_member_use
    expect(s.textScaleFactor, closeTo(2 * 1.15, 1e-9));
    expect(s, const ScaledTextScaler(TextScaler.linear(2), 1.15));
    expect(
      s.hashCode,
      const ScaledTextScaler(TextScaler.linear(2), 1.15).hashCode,
    );
    expect(s, isNot(const ScaledTextScaler(base, 1.3)));
    expect(s, isNot(const ScaledTextScaler(TextScaler.noScaling, 1.15)));
  });
}
