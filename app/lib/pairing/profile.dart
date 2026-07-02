import '../transport/library_key.dart';
import '../transport/ssh_gateway.dart';
import 'pairing_uri.dart';

enum ProfileMode { direct, ssh }

/// A saved connection. Direct profiles carry a full ws:// [url]; ssh profiles
/// carry structured fields plus a [keyId] into the key library. [id] is the
/// `profile_<id>` storage suffix, so it is not stored inside the record JSON.
class Profile {
  const Profile({
    required this.id,
    required this.name,
    required this.mode,
    required this.token,
    this.url,
    this.host,
    this.user,
    this.sshPort,
    this.gatewayPort,
    this.keyId,
  });

  final String id;
  final String name;
  final ProfileMode mode;
  final String token;
  final String? url; // direct
  final String? host; // ssh
  final String? user; // ssh
  final int? sshPort; // ssh (null => default 22)
  final int? gatewayPort; // ssh (null => kDefaultGatewayPort)
  final String? keyId; // ssh

  Map<String, dynamic> toJson() => {
        'name': name,
        'mode': mode.name,
        'token': token,
        if (url != null) 'url': url,
        if (host != null) 'host': host,
        if (user != null) 'user': user,
        if (sshPort != null) 'sshPort': sshPort,
        if (gatewayPort != null) 'gatewayPort': gatewayPort,
        if (keyId != null) 'keyId': keyId,
      };

  /// The gateway ws url an ssh profile dials, with the default gateway port
  /// applied. Only valid when [mode] is ssh.
  String get sshUrl => buildSshGatewayUrl(
        host: host!,
        user: user,
        sshPort: sshPort,
        gatewayPort: gatewayPort ?? kDefaultGatewayPort,
      );

  factory Profile.fromJson(String id, Map<String, dynamic> j) => Profile(
        id: id,
        name: j['name'] as String,
        mode: ProfileMode.values.byName(j['mode'] as String),
        token: j['token'] as String,
        url: j['url'] as String?,
        host: j['host'] as String?,
        user: j['user'] as String?,
        sshPort: j['sshPort'] as int?,
        gatewayPort: j['gatewayPort'] as int?,
        keyId: j['keyId'] as String?,
      );
}

List<Profile> profilesUsingKey(List<Profile> profiles, String keyId) => profiles
    .where((p) => p.mode == ProfileMode.ssh && p.keyId == keyId)
    .toList();

/// An ssh profile is dangling when its referenced key is gone from the library,
/// so it renders as broken and cannot connect until reassigned.
bool profileIsDangling(Profile p, List<LibraryKey> keys) =>
    p.mode == ProfileMode.ssh && !keys.any((k) => k.id == p.keyId);

/// A starting profile from a scanned/typed gateway url. SSH drafts have no key
/// yet (the user picks one before connecting).
Profile draftFromCredentials(String id, GatewayCredentials c) {
  if (!isSshGatewayUrl(c.url)) {
    return Profile(
        id: id, name: '', mode: ProfileMode.direct, token: c.token, url: c.url);
  }
  final cfg = parseSshGatewayUrl(c.url);
  return Profile(
    id: id,
    name: '',
    mode: ProfileMode.ssh,
    token: c.token,
    host: cfg.host,
    user: cfg.user,
    sshPort: cfg.sshPort,
    gatewayPort: cfg.gatewayPort,
  );
}
