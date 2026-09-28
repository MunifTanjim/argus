import 'package:argus/state/navigation.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('defaults: Home, root path, uncommitted', () {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    expect(c.read(scopeProvider), isNull);
    expect(c.read(workspaceTabProvider('A:w1')), WorkspaceTab.sessions);
    expect(c.read(filesPathProvider('A:w1')), '');
    expect(c.read(changesAgainstProvider('A:w1')), '');
  });

  test('each workspace keeps its own tab', () {
    final c = ProviderContainer();
    addTearDown(c.dispose);
    c.read(workspaceTabProvider('A:w1').notifier).state = WorkspaceTab.files;
    expect(c.read(workspaceTabProvider('A:w1')), WorkspaceTab.files);
    expect(c.read(workspaceTabProvider('A:w2')), WorkspaceTab.sessions);
  });

  test('parentPath', () {
    expect(parentPath('a/b/c'), 'a/b');
    expect(parentPath('a'), '');
    expect(parentPath(''), '');
  });
}
