import 'package:argus/models/project.dart';
import 'package:argus/models/workspace_files.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('parseProjectList reads projects and workspaces', () {
    final list = parseProjectList({
      'projects': [
        {
          'id': 'A:p1',
          'name': 'argus',
          'kind': 'git',
          'dir': '/src/argus',
          'pinned': true,
          'error': 'git failed',
          'node_id': 'A',
          'node_label': 'mbp',
          'workspaces': [
            {
              'id': 'A:w1',
              'dir': '/src/argus',
              'is_main': true,
              'branch': 'main',
              'head': 'abc1234def',
              'target_branch': 'main',
            },
            {
              'id': 'A:w2',
              'dir': '/src/argus/.worktrees/registry/',
              'branch': 'feat/registry',
              'setup': {'state': 'running', 'command': 'make'},
            },
          ],
        },
      ],
    });
    expect(list, hasLength(1));
    final p = list.single;
    expect(p.id, 'A:p1');
    expect(p.isGit, isTrue);
    expect(p.pinned, isTrue);
    expect(p.error, 'git failed');
    expect(p.nodeName, 'mbp');
    expect(p.workspaces.first.isMain, isTrue);
    expect(p.workspaces.first.name, 'argus');
    expect(p.workspaces.last.name, 'registry');
    expect(p.workspaces.last.setupState, 'running');
    expect(p.workspaces.first.setupState, isNull);
  });

  test('missing fields take defaults', () {
    final p = ProjectNode.fromJson({'id': 'x', 'name': 'n', 'kind': 'plain'});
    expect(p.isGit, isFalse);
    expect(p.workspaces, isEmpty);
    expect(p.nodeName, 'this machine');
    expect(parseProjectList(null), isEmpty);
    expect(parseProjectList({'projects': 'bad'}), isEmpty);
  });

  test('lookupWorkspace finds the project and workspace by composite id', () {
    final list = parseProjectList({
      'projects': [
        {
          'id': 'A:p1',
          'name': 'argus',
          'workspaces': [
            {'id': 'A:w1', 'dir': '/a'},
          ],
        },
      ],
    });
    final hit = lookupWorkspace(list, 'A:w1');
    expect(hit?.$1.name, 'argus');
    expect(hit?.$2.dir, '/a');
    expect(lookupWorkspace(list, 'A:w9'), isNull);
  });

  test('workspace file models parse', () {
    final l = DirListing.fromJson({
      'path': 'app',
      'entries': [
        {'name': 'lib', 'path': 'app/lib', 'is_dir': true},
        {'name': 'cfg', 'path': 'app/cfg', 'symlink': true, 'target': '../cfg'},
      ],
    });
    expect(l.path, 'app');
    expect(l.entries.first.isDir, isTrue);
    expect(l.entries.last.symlink, isTrue);
    expect(l.entries.last.target, '../cfg');
    final f = FileContent.fromJson({'path': 'a.go', 'not_shown': true});
    expect(f.notShown, isTrue);
    expect(f.content, '');
    final d = WorkspaceDiff.fromJson({'path': 'a.go', 'diff': '@@ -1 +1 @@'});
    expect(d.diff, '@@ -1 +1 @@');
    expect(d.notShown, isFalse);
  });
}
