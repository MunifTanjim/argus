import 'dart:async';
import 'dart:convert';
import 'dart:typed_data';

import 'package:dartssh2/dartssh2.dart';

import 'ssh_gateway.dart';
import 'ssh_hostkey_store.dart';
import 'ssh_key_store.dart';
import 'ssh_tunnel.dart';

/// The remote command a node SSH link runs unless the profile overrides it.
const defaultNodeSshCommand = 'argus bridge';

/// Where a node is reached over SSH: `ssh://[user@]host[:port]`.
class SshNodeTarget {
  const SshNodeTarget({required this.host, this.user, this.sshPort});

  final String host;
  final String? user;
  final int? sshPort; // null => kDefaultSshPort

  /// The host-key pinning key.
  String get hostPort => '$host:${sshPort ?? kDefaultSshPort}';
}

/// Parses a node SSH destination. Mirrors Go `sshconn.ParseSSHTarget`: no path,
/// query or fragment, and no user or host starting with `-`.
SshNodeTarget parseSshNodeTarget(String raw) {
  final Uri u;
  try {
    u = Uri.parse(raw.trim());
  } on FormatException catch (e) {
    throw FormatException('parse ssh url: ${e.message}', raw);
  }
  if (u.scheme != 'ssh') throw FormatException('not an ssh url', raw);
  if (u.host.isEmpty) throw FormatException('ssh url needs a host', raw);
  if ((u.path.isNotEmpty && u.path != '/') || u.hasQuery || u.hasFragment) {
    throw FormatException('ssh url takes no path or query', raw);
  }
  rejectDashTarget(u.userInfo, u.host, raw);
  if (u.hasPort && (u.port < 1 || u.port > 65535)) {
    throw FormatException('ssh port must be in 1..65535', raw);
  }
  return SshNodeTarget(
    host: u.host,
    user: u.userInfo.isEmpty ? null : u.userInfo,
    sshPort: u.hasPort ? u.port : null,
  );
}

/// A profile's node SSH links: destinations by node id or label, the remote
/// command, and the library key to log in with.
class NodeSshSettings {
  const NodeSshSettings({this.targets = const {}, this.command, this.keyId});

  final Map<String, String> targets;
  final String? command;
  final String? keyId;

  String get effectiveCommand {
    final c = command?.trim() ?? '';
    return c.isEmpty ? defaultNodeSshCommand : c;
  }
}

/// Parses `name = ssh://[user@]host[:port]` lines (blank lines skipped) into
/// destinations by node id or label.
Map<String, String> parseNodeSshLines(String text) {
  final out = <String, String>{};
  for (final raw in const LineSplitter().convert(text)) {
    final line = raw.trim();
    if (line.isEmpty) continue;
    final eq = line.indexOf('=');
    final name = eq < 0 ? '' : line.substring(0, eq).trim();
    final url = eq < 0 ? '' : line.substring(eq + 1).trim();
    if (name.isEmpty || url.isEmpty) {
      throw FormatException('expected "node = ssh://host"', line);
    }
    parseSshNodeTarget(url);
    out[name] = url;
  }
  return out;
}

String formatNodeSshLines(Map<String, String> targets) =>
    [for (final e in targets.entries) '${e.key} = ${e.value}'].join('\n');

/// A node link's byte stream: the node's local-socket protocol.
abstract interface class NodeBridge {
  Stream<Uint8List> get stream;
  void add(List<int> data);
  Future<void> close();
}

/// The user did not trust a node host's first-seen key.
class HostKeyDeclinedException extends SshTunnelException {
  HostKeyDeclinedException(super.message);
}

/// The host-key check of one node SSH connection: pinned keys must match, a
/// first-seen key needs [confirm]. [failure] says why a key was refused.
class NodeHostKeyCheck {
  NodeHostKeyCheck(this._store, this.hostPort, this._confirm);

  final HostKeyStore _store;
  final String hostPort;
  final Future<bool> Function(String fingerprint) _confirm;
  SshTunnelException? failure;

  Future<bool> verify(String type, String fingerprint) async {
    final HostKeyDecision d;
    try {
      d = await verifyHostKey(
        _store,
        hostPort,
        type,
        fingerprint,
        confirmUnseen: _confirm,
      );
    } catch (e) {
      failure = SshTunnelException('host key check failed for $hostPort: $e');
      return false;
    }
    switch (d) {
      case HostKeyDecision.accept:
        return true;
      case HostKeyDecision.reject:
        failure = authFailureException(
          hostPort,
          hostKeyRejected: true,
          cause: 'host key changed',
        );
      case HostKeyDecision.declined:
        failure = HostKeyDeclinedException(
          'host key for $hostPort ($fingerprint) was not trusted',
        );
    }
    return false;
  }
}

typedef SshBridgeOpen =
    Future<NodeBridge> Function(
      SshNodeTarget t,
      SshKey key,
      HostKeyStore hk, {
      String command,
      required Future<bool> Function(String fingerprint) confirmHostKey,
      Duration timeout,
    });

/// A remote command's stdio over SSH. Closing it closes the exec channel and
/// the SSH connection.
class SshBridge implements NodeBridge {
  SshBridge._(this._client, this._session) {
    // Unread stderr would pile up.
    _session.stderr.listen((_) {}, onError: (Object _) {});
  }

  final SSHClient _client;
  final SSHSession _session;
  bool _closed = false;

  static Future<SshBridge> open(
    SshNodeTarget t,
    SshKey key,
    HostKeyStore hk, {
    String command = defaultNodeSshCommand,
    required Future<bool> Function(String fingerprint) confirmHostKey,
    Duration timeout = const Duration(seconds: 15),
  }) async {
    final identities = parseIdentities(key);
    final hostPort = t.hostPort;
    final check = NodeHostKeyCheck(hk, hostPort, confirmHostKey);
    final socket = await SSHSocket.connect(
      t.host,
      t.sshPort ?? kDefaultSshPort,
      timeout: timeout,
    );
    final SSHClient client;
    try {
      client = SSHClient(
        socket,
        username: t.user ?? 'root',
        identities: identities,
        onVerifyHostKey: (type, fingerprint) =>
            check.verify(type, utf8.decode(fingerprint)),
      );
    } catch (_) {
      socket.destroy();
      rethrow;
    }
    try {
      await client.authenticated.timeout(timeout);
    } on TimeoutException {
      client.close();
      throw authTimeoutException(hostPort, timeout);
    } catch (e) {
      client.close();
      throw check.failure ??
          authFailureException(hostPort, hostKeyRejected: false, cause: e);
    }
    try {
      return SshBridge._(
        client,
        await client.execute(command).timeout(timeout),
      );
    } catch (_) {
      client.close();
      rethrow;
    }
  }

  @override
  Stream<Uint8List> get stream => _session.stdout;

  @override
  void add(List<int> data) {
    if (_closed) return;
    try {
      _session.stdin.add(Uint8List.fromList(data));
    } catch (_) {
      // The channel already ended; its stream's end closes the link.
    }
  }

  @override
  Future<void> close() async {
    if (_closed) return;
    _closed = true;
    try {
      _session.close();
    } catch (_) {}
    _client.close();
  }
}
