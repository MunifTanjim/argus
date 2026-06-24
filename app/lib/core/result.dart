/// A sealed result type: a call either succeeds with a value ([Ok]) or fails
/// with an error ([Error]). Used by services so that callers must handle the
/// failure path explicitly instead of relying on thrown exceptions or silent
/// no-ops.
///
/// See https://docs.flutter.dev/app-architecture/design-patterns/result.
sealed class Result<T> {
  const Result();

  /// A successful result carrying [value].
  const factory Result.ok(T value) = Ok<T>;

  /// A failed result carrying [error].
  const factory Result.error(Object error) = Error<T>;
}

/// A successful [Result] holding [value].
final class Ok<T> extends Result<T> {
  const Ok(this.value);
  final T value;
}

/// A failed [Result] holding [error].
final class Error<T> extends Result<T> {
  const Error(this.error);
  final Object error;
}
