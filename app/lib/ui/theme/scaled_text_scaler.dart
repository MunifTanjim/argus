import 'package:flutter/widgets.dart';

/// Multiplies [base] (the OS text scaler) by [factor], keeping the base's
/// nonlinear scaling.
class ScaledTextScaler extends TextScaler {
  const ScaledTextScaler(this.base, this.factor);

  final TextScaler base;
  final double factor;

  @override
  double scale(double fontSize) => base.scale(fontSize) * factor;

  @override
  // ignore: deprecated_member_use
  double get textScaleFactor => base.textScaleFactor * factor;

  @override
  bool operator ==(Object other) =>
      other is ScaledTextScaler && other.base == base && other.factor == factor;

  @override
  int get hashCode => Object.hash(base, factor);

  @override
  String toString() => 'ScaledTextScaler($base, x$factor)';
}
