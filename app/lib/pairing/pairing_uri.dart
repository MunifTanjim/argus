class GatewayCredentials {
  final String url;
  final String token;
  final bool e2eEnabled;
  const GatewayCredentials(this.url, this.token, {this.e2eEnabled = false});
}

String buildPairingUri(GatewayCredentials c) {
  final url = Uri.encodeQueryComponent(c.url);
  final token = Uri.encodeQueryComponent(c.token);
  final suffix = c.e2eEnabled ? '&e2e=true' : '';
  return 'argus://pair?url=$url&token=$token$suffix';
}

GatewayCredentials? parsePairingUri(String raw) {
  final uri = Uri.tryParse(raw.trim());
  if (uri == null) return null;
  if (uri.scheme != 'argus' || uri.host != 'pair') return null;
  final url = uri.queryParameters['url'];
  final token = uri.queryParameters['token'];
  if (url == null || url.isEmpty || token == null || token.isEmpty) return null;
  final e2eEnabled = uri.queryParameters['e2e'] == 'true';
  return GatewayCredentials(url, token, e2eEnabled: e2eEnabled);
}
