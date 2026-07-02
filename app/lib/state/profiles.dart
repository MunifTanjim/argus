import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../pairing/gateway_store.dart';
import '../pairing/pairing_uri.dart';
import '../pairing/profile.dart';
import '../pairing/profile_store.dart';
import '../transport/connection.dart';
import '../transport/key_library_store.dart';
import '../transport/library_key.dart';
import '../transport/ssh_hostkey_store.dart';
import '../transport/ssh_key_store.dart';
import '../transport/ssh_tunnel.dart';
import '../transport/ssh_ws_link.dart';
import '../transport/ws_link.dart';

/// Turn a profile into live credentials. For ssh, resolve the library key and
/// copy it into the existing single-slot active-key store the transport reads;
/// the caller then sets credentialsProvider. Throws if the key is gone.
Future<GatewayCredentials> activateProfile(
  Profile p,
  KeyLibraryStore keys,
  SshKeyStore activeKey,
) async {
  if (p.mode == ProfileMode.direct) {
    final url = p.url;
    if (url == null || url.isEmpty) {
      throw StateError('direct profile has no url');
    }
    return GatewayCredentials(url, p.token);
  }
  final keyId = p.keyId;
  final key = keyId == null ? null : await keys.get(keyId);
  if (key == null) throw StateError('profile references a missing SSH key');
  await activeKey.save(SshKey(key.pem, key.passphrase));
  return GatewayCredentials(p.sshUrl, p.token);
}

/// Verify a profile end-to-end without committing it: open the real link (for
/// ssh, the tunnel + gateway WebSocket) and close it. Returns null on success or
/// a user-facing message. Never throws. Does not touch the active-key slot or
/// credentialsProvider, and does not pin an unseen host key — a probe against a
/// wrong host must not persist trust. It still rejects a changed key for an
/// already-pinned host, so Test surfaces a possible MITM.
Future<String?> verifyConnection(
  Profile p,
  LibraryKey? key,
  HostKeyStore hostKeys, {
  Duration timeout = const Duration(seconds: 15),
}) async {
  try {
    final RpcLink link;
    if (p.mode == ProfileMode.direct) {
      link = await WebSocketRpcLink.connect(
        GatewayCredentials(p.url!, p.token),
        timeout: timeout,
      );
    } else {
      if (key == null) return 'Pick an SSH key';
      link = await SshWebSocketRpcLink.connect(
        GatewayCredentials(p.sshUrl, p.token),
        SshKey(key.pem, key.passphrase),
        hostKeys,
        timeout: timeout,
        pinHostKey: false,
      );
    }
    await link.close();
    return null;
  } on SshTunnelException catch (e) {
    return e.message;
  } catch (e) {
    return '$e';
  }
}

/// Re-resolve the persisted active profile at launch. Returns null (and forgets
/// the stored id) when there is none, the profile is gone, or it can no longer
/// be activated (dangling key) — so the app falls back to the profiles list
/// instead of getting stuck on a connection that can never succeed.
Future<GatewayCredentials?> restoreActiveCredentials(
  ProfileStore profiles,
  KeyLibraryStore keys,
  SshKeyStore activeKey,
) async {
  final id = await profiles.loadActiveId();
  if (id == null) return null;
  final profile = await profiles.get(id);
  if (profile == null) {
    await profiles.clearActiveId();
    return null;
  }
  try {
    return await activateProfile(profile, keys, activeKey);
  } catch (_) {
    await profiles.clearActiveId();
    return null;
  }
}

final keyLibraryStoreProvider =
    Provider<KeyLibraryStore>((ref) => KeyLibraryStore(const FlutterSecureKv()));

final profileStoreProvider =
    Provider<ProfileStore>((ref) => ProfileStore(const FlutterSecureKv()));

/// Shared CRUD notifier: mutate the store, then re-list so [state] mirrors it.
abstract class _CrudNotifier<T> extends AsyncNotifier<List<T>> {
  Future<List<T>> listAll();
  Future<void> addOne(T value);
  Future<void> updateOne(T value);
  Future<void> deleteOne(String id);

  @override
  Future<List<T>> build() => listAll();

  Future<void> _refresh() async => state = AsyncData(await listAll());

  Future<void> add(T value) async {
    await addOne(value);
    await _refresh();
  }

  Future<void> save(T value) async {
    await updateOne(value);
    await _refresh();
  }

  Future<void> remove(String id) async {
    await deleteOne(id);
    await _refresh();
  }
}

class KeysNotifier extends _CrudNotifier<LibraryKey> {
  KeyLibraryStore get _store => ref.read(keyLibraryStoreProvider);
  @override
  Future<List<LibraryKey>> listAll() => _store.list();
  @override
  Future<void> addOne(LibraryKey k) => _store.add(k);
  @override
  Future<void> updateOne(LibraryKey k) => _store.update(k);
  @override
  Future<void> deleteOne(String id) => _store.delete(id);
}

final keysProvider =
    AsyncNotifierProvider<KeysNotifier, List<LibraryKey>>(KeysNotifier.new);

class ProfilesNotifier extends _CrudNotifier<Profile> {
  ProfileStore get _store => ref.read(profileStoreProvider);
  @override
  Future<List<Profile>> listAll() => _store.list();
  @override
  Future<void> addOne(Profile p) => _store.add(p);
  @override
  Future<void> updateOne(Profile p) => _store.update(p);
  @override
  Future<void> deleteOne(String id) => _store.delete(id);
}

final profilesProvider =
    AsyncNotifierProvider<ProfilesNotifier, List<Profile>>(ProfilesNotifier.new);

/// In-memory result of the last connection Test per profile id (true = passed).
/// Session-scoped: resets on app restart. Drives the connection row dot.
class ConnectionTestResults extends Notifier<Map<String, bool>> {
  @override
  Map<String, bool> build() => const {};

  void set(String id, bool ok) => state = {...state, id: ok};

  void clear(String id) {
    final m = Map<String, bool>.of(state)..remove(id);
    state = m;
  }
}

final connectionTestResultsProvider =
    NotifierProvider<ConnectionTestResults, Map<String, bool>>(
        ConnectionTestResults.new);

/// Mark the active profile verified after a real successful connect, so its
/// connection dot turns green — the same signal as a passed Test.
Future<void> markActiveConnected(
    ProfileStore profiles, ConnectionTestResults results) async {
  final id = await profiles.loadActiveId();
  if (id != null) results.set(id, true);
}
