import 'package:flutter_test/flutter_test.dart';
import 'package:argus/pairing/pairing_uri.dart';

void main() {
  test('round-trips a pairing uri', () {
    const c = GatewayCredentials('wss://argus.example.ts.net', 's3cr3t/+=');
    final uri = buildPairingUri(c);
    final parsed = parsePairingUri(uri)!;
    expect(parsed.url, c.url);
    expect(parsed.token, c.token);
  });

  test('rejects a non-pairing uri', () {
    expect(parsePairingUri('https://example.com'), isNull);
    expect(parsePairingUri('argus://other?url=a&token=b'), isNull);
    expect(parsePairingUri('argus://pair?url=a'), isNull);
    expect(parsePairingUri('not a uri at all'), isNull);
  });
}
