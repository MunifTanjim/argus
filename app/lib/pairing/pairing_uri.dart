class GatewayCredentials {
  final String url;
  final String token;
  const GatewayCredentials(this.url, this.token);
}

String buildPairingUri(GatewayCredentials c) {
  final url = Uri.encodeQueryComponent(c.url);
  final token = Uri.encodeQueryComponent(c.token);
  return 'argus://pair?url=$url&token=$token';
}

GatewayCredentials? parsePairingUri(String raw) {
  final uri = Uri.tryParse(raw.trim());
  if (uri == null) return null;
  if (uri.scheme != 'argus' || uri.host != 'pair') return null;
  final url = uri.queryParameters['url'];
  final token = uri.queryParameters['token'];
  if (url == null || url.isEmpty || token == null || token.isEmpty) return null;
  return GatewayCredentials(url, token);
}
