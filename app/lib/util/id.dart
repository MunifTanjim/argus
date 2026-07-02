import 'dart:math';

int _counter = 0;
final _rand = Random.secure();

/// A locally-unique id for a new profile/key record. Combines a microsecond
/// timestamp, a per-run counter, and a random suffix — the random part removes
/// the cross-run collision window two records created in the same microsecond
/// on separate runs would otherwise have.
String newId() {
  _counter++;
  final rand = _rand.nextInt(1 << 32).toRadixString(36);
  final ts = DateTime.now().microsecondsSinceEpoch.toRadixString(36);
  return '${ts}_${_counter}_$rand';
}
