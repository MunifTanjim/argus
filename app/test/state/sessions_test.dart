import 'dart:convert';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/registry_event.dart';
import 'package:argus/state/sessions.dart';

const _s1 =
    '{"id":"mac:%1","agent":"t","status":"working","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"node_label":"mac"}';
const _s1awaiting =
    '{"id":"mac:%1","agent":"t","status":"awaiting_input","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"node_label":"mac"}';

const _sA1 =
    '{"id":"A:s1","agent":"t","status":"working","source":"hooked","tmux":{"server":"argus","pane_id":"%1","session_name":"s","window_index":0,"current_path":"/p"},"node_id":"A"}';
const _sA3 =
    '{"id":"A:s3","agent":"t","status":"working","source":"hooked","tmux":{"server":"argus","pane_id":"%3","session_name":"s","window_index":0,"current_path":"/p"},"node_id":"A"}';
const _sB2 =
    '{"id":"B:s2","agent":"t","status":"working","source":"hooked","tmux":{"server":"argus","pane_id":"%2","session_name":"s","window_index":0,"current_path":"/p"},"node_id":"B"}';

void main() {
  test('parseSessions handles a list and null', () {
    final list = parseSessions(jsonDecode('[$_s1]'));
    expect(list.single.id, 'mac:%1');
    expect(parseSessions(null), isEmpty);
  });

  test('applyEvent upserts and removes', () {
    final added = RegistryEvent.fromJson(
      jsonDecode('{"type":"added","session":$_s1}'),
    );
    var m = applyEvent(const {}, added);
    expect(m['mac:%1']!.id, 'mac:%1');

    final updated = RegistryEvent.fromJson(
      jsonDecode('{"type":"updated","session":$_s1awaiting}'),
    );
    m = applyEvent(m, updated);
    expect(m['mac:%1']!.status.name, 'awaitingInput');

    final removed = RegistryEvent.fromJson(
      jsonDecode('{"type":"removed","session":$_s1}'),
    );
    m = applyEvent(m, removed);
    expect(m.containsKey('mac:%1'), isFalse);
  });

  test('notifier replaceAll then apply', () {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final n = c.read(sessionsProvider.notifier);

    n.replaceAll(parseSessions(jsonDecode('[$_s1]')));
    expect(c.read(sessionsProvider).length, 1);

    n.apply(
      RegistryEvent.fromJson(jsonDecode('{"type":"removed","session":$_s1}')),
    );
    expect(c.read(sessionsProvider), isEmpty);
  });

  test('clear empties the store', () {
    final container = ProviderContainer();
    addTearDown(container.dispose);
    final n = container.read(sessionsProvider.notifier);
    n.replaceAll(parseSessions(jsonDecode('[$_s1]')));
    expect(container.read(sessionsProvider), isNotEmpty);
    n.clear();
    expect(container.read(sessionsProvider), isEmpty);
  });

  test('mergeNode replaces only that node\'s slice', () {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final n = c.read(sessionsProvider.notifier);
    n.replaceAll(parseSessions(jsonDecode('[$_sA1,$_sB2]')));
    expect(c.read(sessionsProvider).length, 2);

    n.mergeNode('A', parseSessions(jsonDecode('[$_sA3]')));

    final state = c.read(sessionsProvider);
    expect(state.length, 2);
    expect(state.containsKey('A:s1'), isFalse);
    expect(state.containsKey('A:s3'), isTrue);
    expect(state.containsKey('B:s2'), isTrue);
  });

  test('retainNodes drops sessions of absent nodes', () {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final n = c.read(sessionsProvider.notifier);
    n.replaceAll(parseSessions(jsonDecode('[$_sA1,$_sB2]')));
    expect(c.read(sessionsProvider).length, 2);

    n.retainNodes({'A'});

    final state = c.read(sessionsProvider);
    expect(state.length, 1);
    expect(state.containsKey('A:s1'), isTrue);
    expect(state.containsKey('B:s2'), isFalse);
  });
}
