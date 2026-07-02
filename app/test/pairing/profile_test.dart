import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/pairing_uri.dart';
import 'package:argus/pairing/profile.dart';
import 'package:argus/transport/library_key.dart';

void main() {
  test('ssh profile round-trips through json', () {
    const p = Profile(
      id: 'p1', name: 'box', mode: ProfileMode.ssh, token: 'tok',
      host: 'h', user: 'me', sshPort: 2222, gatewayPort: 8443, keyId: 'k1',
    );
    final back = Profile.fromJson('p1', p.toJson());
    expect(back.mode, ProfileMode.ssh);
    expect(back.host, 'h');
    expect(back.user, 'me');
    expect(back.sshPort, 2222);
    expect(back.gatewayPort, 8443);
    expect(back.keyId, 'k1');
    expect(back.token, 'tok');
  });

  test('direct profile keeps its url', () {
    const p = Profile(
        id: 'p2', name: 'd', mode: ProfileMode.direct, token: 't',
        url: 'ws://h:8443');
    expect(Profile.fromJson('p2', p.toJson()).url, 'ws://h:8443');
  });

  test('profilesUsingKey matches only ssh profiles with that keyId', () {
    const a = Profile(id: 'a', name: 'a', mode: ProfileMode.ssh, token: 't', host: 'h', keyId: 'k1');
    const b = Profile(id: 'b', name: 'b', mode: ProfileMode.ssh, token: 't', host: 'h', keyId: 'k2');
    const c = Profile(id: 'c', name: 'c', mode: ProfileMode.direct, token: 't', url: 'ws://h');
    expect(profilesUsingKey([a, b, c], 'k1').map((p) => p.id), ['a']);
  });

  test('profileIsDangling true when ssh key is absent from library', () {
    const p = Profile(id: 'a', name: 'a', mode: ProfileMode.ssh, token: 't', host: 'h', keyId: 'gone');
    const present = LibraryKey(id: 'k1', name: 'k', pem: 'P');
    expect(profileIsDangling(p, const [present]), isTrue);
    const okp = Profile(id: 'a', name: 'a', mode: ProfileMode.ssh, token: 't', host: 'h', keyId: 'k1');
    expect(profileIsDangling(okp, const [present]), isFalse);
  });

  test('direct profiles are never dangling', () {
    const p = Profile(id: 'a', name: 'a', mode: ProfileMode.direct, token: 't', url: 'ws://h');
    expect(profileIsDangling(p, const []), isFalse);
  });

  test('draftFromCredentials parses an ssh url into fields', () {
    final p = draftFromCredentials(
        'p1', const GatewayCredentials('ssh://me@h:2222?port=9000', 'tok'));
    expect(p.mode, ProfileMode.ssh);
    expect(p.host, 'h');
    expect(p.user, 'me');
    expect(p.sshPort, 2222);
    expect(p.gatewayPort, 9000);
    expect(p.keyId, isNull);
    expect(p.token, 'tok');
  });

  test('draftFromCredentials keeps a ws url as a direct profile', () {
    final p = draftFromCredentials(
        'p1', const GatewayCredentials('ws://h:8443', 'tok'));
    expect(p.mode, ProfileMode.direct);
    expect(p.url, 'ws://h:8443');
  });
}
