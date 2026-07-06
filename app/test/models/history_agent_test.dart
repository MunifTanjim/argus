import 'dart:convert';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/history.dart';

void main() {
  test('HistorySession parses agent', () {
    final s = HistorySession.fromJson(jsonDecode(
        '{"session_id":"x","transcript_path":"/p","last_activity":"2026-01-01T00:00:00Z","agent":"codex"}') as Map<String, dynamic>);
    expect(s.agent, 'codex');
  });
}
