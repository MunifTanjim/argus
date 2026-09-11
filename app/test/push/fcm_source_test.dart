import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/push/fcm_source.dart';

void main() {
  group('decodeFcmBody', () {
    test('reads the PushPort `e` key (base64url, unpadded)', () {
      final bytes = Uint8List.fromList([1, 2, 3, 250, 0, 255, 128, 64, 200]);
      // Mirror PushPort's base64.RawURLEncoding.
      final e = base64Url.encode(bytes).replaceAll('=', '');
      expect(decodeFcmBody({'e': e}), bytes);
    });

    test('returns null when `e` is absent (e.g. only a legacy `body` key)', () {
      expect(decodeFcmBody({'body': 'AQID'}), isNull);
    });

    test('returns null on malformed base64', () {
      expect(decodeFcmBody({'e': '@@not-base64@@'}), isNull);
    });

    test('returns null when `e` is empty', () {
      expect(decodeFcmBody({'e': ''}), isNull);
    });
  });
}
