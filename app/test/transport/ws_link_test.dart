import 'package:flutter_test/flutter_test.dart';
import 'package:argus/transport/ws_link.dart';

void main() {
  group('resolveClientUrl', () {
    test('appends /client to a base url', () {
      expect(resolveClientUrl('wss://argus.example.ts.net'),
          'wss://argus.example.ts.net/client');
      expect(resolveClientUrl('ws://192.168.1.5:8443'),
          'ws://192.168.1.5:8443/client');
    });

    test('treats a bare trailing slash as no path', () {
      expect(resolveClientUrl('wss://host/'), 'wss://host/client');
    });

    test('rejects a non-empty path (route is implicit)', () {
      expect(() => resolveClientUrl('wss://host/client'), throwsFormatException);
      expect(() => resolveClientUrl('wss://host/x'), throwsFormatException);
    });

    test('rejects bad scheme or missing host', () {
      expect(() => resolveClientUrl('https://host'), throwsFormatException);
      expect(() => resolveClientUrl('wss://'), throwsFormatException);
    });
  });
}
