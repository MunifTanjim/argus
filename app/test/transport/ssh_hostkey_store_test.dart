import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/transport/ssh_hostkey_store.dart';

class MemKv implements SecureKv {
  final _m = <String, String>{};
  @override
  Future<String?> read(String key) async => _m[key];
  @override
  Future<void> write(String key, String value) async => _m[key] = value;
  @override
  Future<void> delete(String key) async => _m.remove(key);
}

void main() {
  const ed = 'ssh-ed25519';
  const rsa = 'ssh-rsa';

  test('first use pins and accepts', () async {
    final store = HostKeyStore(MemKv());
    final d = await verifyHostKey(store, 'h:22', ed, 'SHA256:AAA');
    expect(d, HostKeyDecision.accept);
    expect(await store.pinned('h:22', ed), 'SHA256:AAA');
  });

  test('matching fingerprint accepts', () async {
    final store = HostKeyStore(MemKv());
    await store.pin('h:22', ed, 'SHA256:AAA');
    expect(await verifyHostKey(store, 'h:22', ed, 'SHA256:AAA'),
        HostKeyDecision.accept);
  });

  test('changed fingerprint rejects and does not overwrite the pin', () async {
    final store = HostKeyStore(MemKv());
    await store.pin('h:22', ed, 'SHA256:AAA');
    expect(await verifyHostKey(store, 'h:22', ed, 'SHA256:BBB'),
        HostKeyDecision.reject);
    expect(await store.pinned('h:22', ed), 'SHA256:AAA');
  });

  test('a different key type on the same host pins independently', () async {
    final store = HostKeyStore(MemKv());
    await store.pin('h:22', ed, 'SHA256:AAA');
    // A server negotiating a different key type must not read as a MITM.
    expect(await verifyHostKey(store, 'h:22', rsa, 'SHA256:CCC'),
        HostKeyDecision.accept);
    expect(await store.pinned('h:22', ed), 'SHA256:AAA');
    expect(await store.pinned('h:22', rsa), 'SHA256:CCC');
  });

  test('pinUnseen:false accepts an unseen key without persisting it', () async {
    final store = HostKeyStore(MemKv());
    expect(
        await verifyHostKey(store, 'h:22', ed, 'SHA256:AAA', pinUnseen: false),
        HostKeyDecision.accept);
    expect(await store.pinned('h:22', ed), isNull);
  });

  test('pinUnseen:false still rejects a changed key for a pinned host',
      () async {
    final store = HostKeyStore(MemKv());
    await store.pin('h:22', ed, 'SHA256:AAA');
    expect(
        await verifyHostKey(store, 'h:22', ed, 'SHA256:BBB', pinUnseen: false),
        HostKeyDecision.reject);
  });

  test('a legacy plain-string pin is tolerated, not thrown on', () async {
    // Pre-fix versions stored a bare fingerprint (not JSON). Reading it must
    // not throw inside onVerifyHostKey (which surfaces as an opaque
    // "connection closed before authentication"); treat it as unpinned so TOFU
    // re-pins in the new per-type JSON format.
    final kv = MemKv();
    await kv.write('ssh_hostkey_h:22', 'SHA256:LEGACY');
    final store = HostKeyStore(kv);
    expect(await store.pinned('h:22', ed), isNull);
    expect(await verifyHostKey(store, 'h:22', ed, 'SHA256:AAA'),
        HostKeyDecision.accept);
    expect(await store.pinned('h:22', ed), 'SHA256:AAA');
  });

  test('forget clears the pin so the next connect re-pins', () async {
    final store = HostKeyStore(MemKv());
    await store.pin('h:22', ed, 'SHA256:AAA');
    await store.forget('h:22');
    expect(await store.pinned('h:22', ed), isNull);
    expect(await verifyHostKey(store, 'h:22', ed, 'SHA256:BBB'),
        HostKeyDecision.accept);
  });
}
