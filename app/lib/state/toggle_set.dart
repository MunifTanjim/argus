import 'package:flutter_riverpod/flutter_riverpod.dart';

/// A set of items that a tap adds or removes, such as folded groups or
/// selected filters. Starts empty.
class ToggleSet<T> extends Notifier<Set<T>> {
  @override
  Set<T> build() => const {};

  void toggle(T item) => state = state.contains(item)
      ? ({...state}..remove(item))
      : {...state, item};

  void clear() => state = const {};
}
