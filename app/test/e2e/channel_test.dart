import 'dart:convert';

import 'package:flutter_test/flutter_test.dart';
import 'package:argus/e2e/channel.dart';

void main() {
  test('plain channel round-trips params', () {
    final c = Channel.plain('c1');
    final params = utf8.encode('{"session_id":"s1"}');
    final frame = c.sealRequestFrame(1, 'sessions.tasks', 'node-a', params);
    final f = RelayFrame.parse(frame);
    expect(c.openParams(f), equals(params));
  });
}
