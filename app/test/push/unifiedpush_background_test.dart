import 'dart:convert';
import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';

import 'package:argus/push/unifiedpush_background.dart' show decodeUnifiedPush;

void main() {
  group('decodeUnifiedPush', () {
    Uint8List encode(Map<String, dynamic> obj) =>
        Uint8List.fromList(utf8.encode(jsonEncode(obj)));

    test('composites session_id when node_id is present', () {
      final bytes = encode({
        'id': 'm1',
        'title': 't',
        'body': 'b',
        'data': {'session_id': 's1', 'node_id': 'n1'},
      });
      final result = decodeUnifiedPush(bytes);
      expect(result.data['session_id'], 'n1:s1');
      expect(result.sessionId, 'n1:s1');
    });

    test('leaves session_id bare when node_id absent', () {
      final bytes = encode({
        'id': 'm2',
        'title': 't',
        'body': 'b',
        'data': {'session_id': 's1'},
      });
      final result = decodeUnifiedPush(bytes);
      expect(result.sessionId, 's1');
    });

    test('malformed bytes fall back to a body-only message', () {
      final bytes = Uint8List.fromList([0xff, 0xfe]);
      final result = decodeUnifiedPush(bytes);
      expect(result.sessionId, isNull);
    });
  });
}
