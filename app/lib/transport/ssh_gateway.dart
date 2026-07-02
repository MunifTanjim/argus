/// Parsing and building for the `ssh://` gateway scheme, mirroring Go's
/// `resolveGatewayURL` (cmd/argus/gateway.go). The app tunnels its WebSocket to
/// `127.0.0.1:<gatewayPort>` on the SSH host, so an ssh url takes no path.
const int kDefaultGatewayPort = 8443;
const int kDefaultSshPort = 22;

class SshGatewayConfig {
  const SshGatewayConfig({
    required this.host,
    this.user,
    this.sshPort,
    required this.gatewayPort,
  });

  final String host;
  final String? user;
  final int? sshPort; // null => kDefaultSshPort
  final int gatewayPort;
}

bool isSshGatewayUrl(String raw) => Uri.tryParse(raw.trim())?.scheme == 'ssh';

/// Parse [s] as a TCP port (1..65535), throwing [FormatException] otherwise.
/// Uses radix 10 so non-decimal input (e.g. `0x10`) is rejected rather than
/// silently reinterpreted, and range-checks so negatives/zero/overflow don't
/// slip through to fail opaquely at socket-bind time.
int parsePort(String s, {String? source}) {
  final p = int.tryParse(s.trim(), radix: 10);
  if (p == null || p < 1 || p > 65535) {
    throw FormatException('port must be an integer in 1..65535', source ?? s);
  }
  return p;
}

SshGatewayConfig parseSshGatewayUrl(String raw) {
  final u = Uri.parse(raw.trim());
  if (u.scheme != 'ssh') {
    throw FormatException('gateway url must be ssh://', raw);
  }
  if (u.host.isEmpty) {
    throw FormatException('ssh gateway url has no host', raw);
  }
  if (u.path.isNotEmpty && u.path != '/') {
    throw FormatException(
        'ssh gateway url takes no path (it dials the gateway directly)', raw);
  }
  final portParam = u.queryParameters['port'];
  final gatewayPort = (portParam == null || portParam.isEmpty)
      ? kDefaultGatewayPort
      : parsePort(portParam, source: raw);
  // Uri parses the port's digits but does not range-check it; enforce here so a
  // bogus ssh port is rejected at parse time, not at connect time.
  final int? sshPort;
  if (u.hasPort) {
    if (u.port < 1 || u.port > 65535) {
      throw FormatException('ssh port must be in 1..65535', raw);
    }
    sshPort = u.port;
  } else {
    sshPort = null;
  }
  return SshGatewayConfig(
    host: u.host,
    user: u.userInfo.isEmpty ? null : u.userInfo,
    sshPort: sshPort,
    gatewayPort: gatewayPort,
  );
}

String buildSshGatewayUrl({
  required String host,
  String? user,
  int? sshPort,
  required int gatewayPort,
}) {
  final auth = (user != null && user.isNotEmpty) ? '$user@$host' : host;
  final port = sshPort != null ? ':$sshPort' : '';
  return 'ssh://$auth$port?port=$gatewayPort';
}

String sshHostPort(SshGatewayConfig cfg) =>
    '${cfg.host}:${cfg.sshPort ?? kDefaultSshPort}';
