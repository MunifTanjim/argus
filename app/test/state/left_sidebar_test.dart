import 'dart:async';

import 'package:argus/pairing/gateway_store.dart';
import 'package:argus/state/left_sidebar.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

class _FakeKv implements SecureKv {
  _FakeKv(this.m, {this.gate});
  final Map<String, String> m;
  final Completer<void>? gate;
  @override
  Future<String?> read(String key) async {
    await gate?.future;
    return m[key];
  }

  @override
  Future<void> write(String key, String value) async => m[key] = value;
  @override
  Future<void> delete(String key) async => m.remove(key);
}

ProviderContainer _container(_FakeKv kv) {
  final c = ProviderContainer(overrides: [
    leftSidebarTabStoreProvider.overrideWithValue(LeftSidebarTabStore(kv)),
  ]);
  addTearDown(c.dispose);
  return c;
}

void main() {
  test('loads the saved tab and saves a new one', () async {
    final kv = _FakeKv({'leftSidebar.tab': 'sessions'});
    final c = _container(kv);
    expect(c.read(leftSidebarTabProvider), LeftSidebarTab.projects);
    await Future<void>.delayed(Duration.zero);
    expect(c.read(leftSidebarTabProvider), LeftSidebarTab.sessions);
    await c.read(leftSidebarTabProvider.notifier).set(LeftSidebarTab.projects);
    expect(kv.m['leftSidebar.tab'], 'projects');
  });

  test('unknown value falls back to projects', () async {
    expect(
      await LeftSidebarTabStore(_FakeKv({'leftSidebar.tab': 'files'})).load(),
      LeftSidebarTab.projects,
    );
  });

  test('choice during load wins', () async {
    final gate = Completer<void>();
    final c = _container(_FakeKv({'leftSidebar.tab': 'projects'}, gate: gate));
    c.read(leftSidebarTabProvider);
    await c.read(leftSidebarTabProvider.notifier).set(LeftSidebarTab.sessions);
    gate.complete();
    await Future<void>.delayed(Duration.zero);
    expect(c.read(leftSidebarTabProvider), LeftSidebarTab.sessions);
  });
}
