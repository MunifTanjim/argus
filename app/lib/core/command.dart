import 'package:flutter/foundation.dart';

import 'result.dart';

typedef CommandAction0<T> = Future<Result<T>> Function();
typedef CommandAction1<T, A> = Future<Result<T>> Function(A arg);

/// Wraps a single asynchronous action and exposes its lifecycle as listenable
/// state: [running] while in flight, [completed]/[error] once the [Result] is
/// in. Guards against concurrent re-entry (a second [execute] while [running]
/// is ignored) and captures the result so the UI can react.
///
/// See https://docs.flutter.dev/app-architecture/design-patterns/command.
abstract class Command<T> extends ChangeNotifier {
  bool _running = false;
  Result<T>? _result;

  bool get running => _running;

  /// The last result, or null before the first run / after [clearResult].
  Result<T>? get result => _result;

  bool get completed => _result is Ok;
  bool get error => _result is Error;

  Future<void> _run(CommandAction0<T> action) async {
    if (_running) return;
    _running = true;
    _result = null;
    notifyListeners();
    try {
      _result = await action();
    } finally {
      _running = false;
      notifyListeners();
    }
  }

  /// Clears the captured result (e.g. after showing an error once).
  void clearResult() {
    _result = null;
    notifyListeners();
  }
}

/// A [Command] for an action that takes no arguments.
class Command0<T> extends Command<T> {
  Command0(this._action);
  final CommandAction0<T> _action;

  Future<void> execute() => _run(_action);
}

/// A [Command] for an action that takes one argument.
class Command1<T, A> extends Command<T> {
  Command1(this._action);
  final CommandAction1<T, A> _action;

  Future<void> execute(A arg) => _run(() => _action(arg));
}
