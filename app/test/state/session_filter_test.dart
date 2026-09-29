import 'dart:convert';

import 'package:argus/models/session.dart';
import 'package:argus/state/session_filter.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

Session _s(String status) => Session.fromJson(
  jsonDecode(
    '{"id":"n:1","agent":"t","status":"$status","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"repo":"r"}',
  ),
);

void main() {
  test('active sessions are the running and awaiting ones', () {
    for (final st in ['discovered', 'starting', 'working', 'awaiting_input']) {
      expect(isActiveSession(_s(st)), isTrue, reason: st);
    }
    for (final st in ['idle', 'dead', 'unknown']) {
      expect(isActiveSession(_s(st)), isFalse, reason: st);
    }
  });

  test('starts on all and toggles', () {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    expect(c.read(activeOnlyProvider), isFalse);
    c.read(activeOnlyProvider.notifier).toggle();
    expect(c.read(activeOnlyProvider), isTrue);
  });
}
