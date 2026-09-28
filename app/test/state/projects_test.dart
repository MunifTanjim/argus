import 'dart:async';

import 'package:argus/state/projects.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

import '../support/fake_gateway_client.dart';

Map<String, dynamic> _list(String name) => {
  'projects': [
    {'id': 'A:$name', 'name': name, 'workspaces': []},
  ],
};

void main() {
  test('load parses the merged list', () async {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    await c
        .read(projectsProvider.notifier)
        .load(FakeGatewayClient((m, p) async => _list('argus')));
    final s = c.read(projectsProvider);
    expect(s.loaded, isTrue);
    expect(s.error, isNull);
    expect(s.projects.single.name, 'argus');
  });

  test('failed load keeps projects', () async {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final n = c.read(projectsProvider.notifier);
    await n.load(FakeGatewayClient((m, p) async => _list('argus')));
    await n.load(FakeGatewayClient((m, p) async => throw StateError('down')));
    final s = c.read(projectsProvider);
    expect(s.projects.single.name, 'argus');
    expect(s.loaded, isTrue);
    expect(s.error, contains('down'));
  });

  test(
    'a partial failure keeps the failed node\'s previous projects',
    () async {
      final c = ProviderContainer();
      addTearDown(c.dispose);
      final n = c.read(projectsProvider.notifier);
      Map<String, dynamic> p(String node, String name) => {
        'id': '$node:$name',
        'name': name,
        'node_id': node,
        'workspaces': [],
      };
      await n.load(
        FakeGatewayClient(
          (m, _) async => {
            'projects': [p('A', 'argus'), p('B', 'infra'), p('C', 'old')],
          },
        ),
      );
      // B failed; C is no longer connected, so it is absent from the fan-out.
      await n.load(
        FakeGatewayClient(
          (m, _) async => {
            'projects': [p('A', 'argus2')],
            'failed_nodes': ['B'],
          },
        ),
      );
      final s = c.read(projectsProvider);
      expect(s.projects.map((p) => p.id), ['A:argus2', 'B:infra']);
      expect(s.error, isNull);
    },
  );

  test('a stale load does not overwrite a newer one', () async {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final n = c.read(projectsProvider.notifier);
    final slow = Completer<Object?>();
    final first = n.load(FakeGatewayClient((m, p) => slow.future));
    await n.load(FakeGatewayClient((m, p) async => _list('new')));
    slow.complete(_list('old'));
    await first;
    expect(c.read(projectsProvider).projects.single.name, 'new');
  });

  test('a null client does nothing, and clear resets', () async {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    final n = c.read(projectsProvider.notifier);
    await n.load(null);
    expect(c.read(projectsProvider).loaded, isFalse);
    await n.load(FakeGatewayClient((m, p) async => _list('argus')));
    n.clear();
    expect(c.read(projectsProvider).projects, isEmpty);
    expect(c.read(projectsProvider).loaded, isFalse);
  });
}
