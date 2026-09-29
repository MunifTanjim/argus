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
    expect(p.workspaces.last.setup?.state, 'running');
    expect(p.workspaces.last.setup?.command, 'make');
    expect(p.workspaces.first.setup, isNull);
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

  test('parses scripts and the full setup run', () {
    final p = ProjectNode.fromJson({
      'id': 'A:p1',
      'name': 'argus',
      'default_branch': 'dev',
      'scripts': {'setup': 'make deps', 'teardown': 'make clean'},
      'workspaces': [
        {
          'id': 'A:w1',
          'dir': '/src/argus',
          'is_main': true,
          'setup': {
            'state': 'failed',
            'command': 'make deps',
            'exit_code': 2,
            'output_tail': 'boom\n',
          },
        },
      ],
    });
    expect(p.setupScript, 'make deps');
    expect(p.defaultBranch, 'dev');
    expect(p.copyWith().defaultBranch, 'dev');
    expect(p.teardownScript, 'make clean');
    final run = p.workspaces.single.setup!;
    expect(run.state, 'failed');
    expect(run.exitCode, 2);
    expect(run.outputTail, 'boom\n');
    expect(ProjectNode.fromJson({'id': 'x', 'name': 'n'}).setupScript, '');
    expect(p.copyWith(workspaces: const []).teardownScript, 'make clean');
  });

  test('lookupProject, mainWorkspace, and baseName', () {
    const main = WorkspaceNode(id: 'A:w1', dir: '/src/argus', isMain: true);
    const other = WorkspaceNode(id: 'A:w2', dir: '/src/argus/.wt/x/');
    const p = ProjectNode(id: 'A:p1', name: 'argus', workspaces: [main, other]);
    expect(lookupProject(const [p], 'A:p1'), same(p));
    expect(lookupProject(const [p], 'A:p9'), isNull);
    expect(mainWorkspace(p), same(main));
    const goneMain = WorkspaceNode(
      id: 'A:w1',
      dir: '/src/argus',
      isMain: true,
      isGone: true,
    );
    expect(
      mainWorkspace(
        const ProjectNode(id: 'p', name: 'n', workspaces: [goneMain]),
      ),
      isNull,
    );
    expect(baseName('/src/argus/.wt/x/'), 'x');
    expect(baseName('/'), '/');
  });
}
