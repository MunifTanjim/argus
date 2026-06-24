import 'dart:convert';

import '../pairing/gateway_store.dart';

/// Tracks recently displayed push message ids so a delivery the UnifiedPush
/// Android plugin replays isn't shown twice. The plugin's event flow buffers the
/// last events (replay=20) and re-emits them to a freshly attached engine when
/// the app's Activity is relaunched (e.g. reopening after the back button) — the
/// same payload, same id, with no fresh push. State is persisted because that
/// replay lands in a new engine/isolate where in-memory state is gone.
class SeenMessages {
  SeenMessages([SecureKv? kv]) : _kv = kv ?? const FlutterSecureKv();

  final SecureKv _kv;

  static const _key = 'push_seen_ids';
  // Bounded so the record can't grow without limit; well above the plugin's
  // replay=20 buffer, so any id it could replay is still remembered.
  static const _max = 50;

  /// Records [id] as seen and returns true if it was new (caller should display)
  /// or false if already seen (a replay — caller should skip). A null/empty id
  /// is always treated as new: without an id there's nothing to dedup on.
  Future<bool> markSeen(String? id) async {
    if (id == null || id.isEmpty) return true;
    final raw = await _kv.read(_key);
    final ids = raw == null
        ? <String>[]
        : (jsonDecode(raw) as List).cast<String>();
    if (ids.contains(id)) return false;
    ids.add(id);
    if (ids.length > _max) ids.removeRange(0, ids.length - _max);
    await _kv.write(_key, jsonEncode(ids));
    return true;
  }
}
