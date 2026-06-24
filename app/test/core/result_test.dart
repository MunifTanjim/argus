// app/test/core/result_test.dart
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/core/result.dart';

void main() {
  test('Result.ok builds an Ok carrying the value', () {
    const Result<int> r = Result.ok(42);
    expect(r, isA<Ok<int>>());
    expect((r as Ok<int>).value, 42);
  });

  test('Result.error builds an Error carrying the error', () {
    final err = StateError('boom');
    final Result<int> r = Result.error(err);
    expect(r, isA<Error<int>>());
    expect((r as Error<int>).error, same(err));
  });

  test('switch is exhaustive over Ok/Error', () {
    String describe(Result<String> r) => switch (r) {
          Ok(:final value) => 'ok:$value',
          Error(:final error) => 'err:$error',
        };
    expect(describe(const Ok('hi')), 'ok:hi');
    expect(describe(Error(Exception('x'))), 'err:Exception: x');
  });

  test('Result<void> ok carries null', () {
    const Result<void> r = Result.ok(null);
    expect(r, isA<Ok<void>>());
  });
}
