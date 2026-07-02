import 'dart:async';
import 'dart:convert';
import 'dart:io';

import 'package:dartssh2/dartssh2.dart';

import 'connection.dart';
import 'ssh_gateway.dart';
import 'ssh_hostkey_store.dart';
import 'ssh_key_store.dart';

class SshTunnelException implements Exception {
  SshTunnelException(this.message);
  final String message;
  @override
  String toString() => 'SshTunnelException: $message';
}

/// The pinned host key changed (a possible MITM). Fatal: reconnecting cannot
/// fix it, so the manager must stop and surface it rather than loop in backoff.
class HostKeyChangedException extends SshTunnelException
    implements FatalConnectError {
  HostKeyChangedException(super.message);
}

/// Parse the private key up front so bad key material fails clearly instead of
/// surfacing as an opaque auth error later.
List<SSHKeyPair> parseIdentities(SshKey key) {
  try {
    return SSHKeyPair.fromPem(key.pem, key.passphrase);
  } catch (e) {
    throw SshTunnelException('invalid SSH key or passphrase: $e');
  }
}

/// Local check that [pem] (+ optional [passphrase]) is a loadable key, without
/// connecting. Returns null when it decrypts, or a user-facing message when it
/// does not (wrong passphrase or malformed key).
String? verifyKey(String pem, String? passphrase) {
  try {
    parseIdentities(SshKey(pem, passphrase));
    return null;
  } on SshTunnelException catch (e) {
    return e.message;
  }
}

/// Map a failed [SSHClient.authenticated] into a user-facing exception. A
/// rejected host key means the pinned fingerprint changed (a possible MITM),
/// which needs a distinct, actionable message from a plain auth failure.
SshTunnelException authFailureException(
  String hostPort, {
  required bool hostKeyRejected,
  required Object cause,
}) {
  if (hostKeyRejected) {
    return HostKeyChangedException(
        'host key changed for $hostPort — possible MITM; forget the pinned '
        'key to reconnect');
  }
  return SshTunnelException('SSH authentication failed: $cause');
}

/// A stalled handshake (half-open socket, unresponsive server) leaves
/// [SSHClient.authenticated] pending forever; surface it as a timeout, distinct
/// from a rejection, so open() can close the client instead of leaking it.
SshTunnelException authTimeoutException(String hostPort, Duration timeout) =>
    SshTunnelException(
        'SSH authentication timed out after ${timeout.inSeconds}s for $hostPort');

/// An SSH connection plus a loopback forwarder. Binds `127.0.0.1:<ephemeral>`
/// on the device and forwards each accepted connection through SSH to
/// `127.0.0.1:<gatewayPort>` on the host (the `ssh -L` pattern), so the plain
/// WebSocket transport can dial [localUrl] unchanged.
class SshTunnel {
  SshTunnel._(this._client, this._server, this.localUrl, this._serverSub);

  final SSHClient _client;
  final ServerSocket _server;
  final StreamSubscription<Socket> _serverSub;

  /// `ws://127.0.0.1:<ephemeral>` — pass to WebSocketRpcLink (which appends /client).
  final String localUrl;

  static Future<SshTunnel> open(
    SshGatewayConfig cfg,
    SshKey key,
    HostKeyStore hostKeys, {
    Duration timeout = const Duration(seconds: 15),
    // "Test connection" probes without persisting trust: an unseen host key is
    // accepted for the probe but not pinned. A real connect pins (TOFU).
    bool pinHostKey = true,
  }) async {
    final identities = parseIdentities(key); // throws SshTunnelException
    final hostPort = sshHostPort(cfg);
    final sshPort = cfg.sshPort ?? kDefaultSshPort;

    final socket = await SSHSocket.connect(cfg.host, sshPort, timeout: timeout);

    // SSHClient takes ownership of the socket and destroys it on close(). Until
    // it is constructed the socket is ours to clean up, so guard construction
    // to avoid leaking the connection if the constructor throws.
    var hostKeyRejected = false;
    final SSHClient client;
    try {
      client = SSHClient(
        socket,
        username: cfg.user ?? 'root',
        identities: identities,
        onVerifyHostKey: (type, fingerprint) async {
          // dartssh2 gives the OpenSSH-style "SHA256:<base64>" fingerprint,
          // UTF-8 encoded.
          final fp = utf8.decode(fingerprint);
          final decision = await verifyHostKey(hostKeys, hostPort, type, fp,
              pinUnseen: pinHostKey);
          if (decision == HostKeyDecision.reject) hostKeyRejected = true;
          return decision == HostKeyDecision.accept;
        },
      );
    } catch (_) {
      socket.destroy();
      rethrow;
    }

    try {
      await client.authenticated.timeout(timeout);
    } on TimeoutException {
      client.close(); // destroys the underlying socket; no leaked stalled auth
      throw authTimeoutException(hostPort, timeout);
    } catch (e) {
      client.close(); // destroys the underlying socket
      throw authFailureException(hostPort,
          hostKeyRejected: hostKeyRejected, cause: e);
    }

    try {
      final server = await ServerSocket.bind(InternetAddress.loopbackIPv4, 0);
      final serverSub = server.listen((sock) async {
        try {
          final ch = await client.forwardLocal('127.0.0.1', cfg.gatewayPort);
          // remote -> local
          ch.stream.listen(
            sock.add,
            onError: (_) => sock.destroy(),
            onDone: () => sock.destroy(),
          );
          // local -> remote
          sock.listen(
            ch.sink.add,
            onError: (_) => ch.sink.close(),
            onDone: () => ch.sink.close(),
          );
        } catch (_) {
          sock.destroy();
        }
      });

      return SshTunnel._(
          client, server, 'ws://127.0.0.1:${server.port}', serverSub);
    } catch (e) {
      client.close();
      rethrow;
    }
  }

  Future<void> close() async {
    await _serverSub.cancel();
    await _server.close();
    _client.close();
  }
}
