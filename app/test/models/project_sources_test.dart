import 'package:argus/models/project_sources.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  test('parses branches, PRs, issues, and the create result', () {
    final b = BranchInfo.fromJson({
      'name': 'feat/x',
      'remote': true,
      'checked_out': true,
    });
    expect(
      (b.name, b.remote, b.local, b.checkedOut),
      ('feat/x', true, false, true),
    );

    final prs = parsePrs({
      'prs': [
        {
          'number': 7,
          'title': 'Fix it',
          'author': 'ann',
          'head_branch': 'fix',
          'base_branch': 'main',
        },
      ],
      'truncated': true,
    });
    expect(prs.truncated, isTrue);
    expect(prs.items.single.headBranch, 'fix');

    final issues = parseIssues({
      'issues': [
        {'number': 3, 'title': 'Crash', 'author': 'bob'},
      ],
    });
    expect(issues.truncated, isFalse);
    expect(issues.items.single.number, 3);

    expect(
      parseBranches({
        'branches': [
          {'name': 'main', 'local': true},
        ],
      }).single.local,
      isTrue,
    );
    expect(parseBranches(null), isEmpty);

    final r = CreateResult.fromJson({
      'workspace_id': 'A:w9',
      'dir': '/src/x',
      'warning': 'w',
      'prompt': 'p',
      'setup': 'make',
    });
    expect(
      (r.workspaceId, r.dir, r.warning, r.prompt, r.setup),
      ('A:w9', '/src/x', 'w', 'p', 'make'),
    );
  });

  test('CreateRequest params follow the node contract', () {
    expect(
      const CreateRequest(source: 'new', branch: 'feat/y').params('A:p1'),
      {'project_id': 'A:p1', 'source': 'new', 'branch': 'feat/y'},
    );
    expect(
      const CreateRequest(
        source: 'branch',
        branch: 'b',
        targetBranch: 'dev',
      ).params('A:p1'),
      {
        'project_id': 'A:p1',
        'source': 'branch',
        'branch': 'b',
        'target_branch': 'dev',
      },
    );
    // A PR create sends no target: the node uses the PR base.
    expect(
      const CreateRequest(
        source: 'pr',
        number: 7,
        targetBranch: 'dev',
      ).params('A:p1'),
      {'project_id': 'A:p1', 'source': 'pr', 'number': 7},
    );
    expect(const CreateRequest(source: 'issue', number: 3).params('A:p1'), {
      'project_id': 'A:p1',
      'source': 'issue',
      'number': 3,
    });
  });

  test('filters are trimmed and case-insensitive over the TUI fields', () {
    const branches = [BranchInfo(name: 'feat/Login'), BranchInfo(name: 'main')];
    expect(filterBranches(branches, '  LOGIN ').single.name, 'feat/Login');
    expect(filterBranches(branches, ''), hasLength(2));

    const prs = [
      PrInfo(
        number: 12,
        title: 'Add X',
        author: 'ann',
        headBranch: 'x',
        baseBranch: 'dev',
      ),
      PrInfo(
        number: 5,
        title: 'Fix Y',
        author: 'bob',
        headBranch: 'y',
        baseBranch: 'main',
      ),
    ];
    expect(filterPrs(prs, '12').single.number, 12);
    expect(filterPrs(prs, 'BOB').single.number, 5);
    expect(filterPrs(prs, 'dev').single.number, 12);
    expect(filterPrs(prs, 'zzz'), isEmpty);

    const issues = [
      IssueInfo(number: 3, title: 'Crash on start', author: 'cy'),
      IssueInfo(number: 4, title: 'Docs', author: 'dee'),
    ];
    expect(filterIssues(issues, 'crash').single.number, 3);
    expect(filterIssues(issues, 'dee').single.number, 4);
    expect(filterIssues(issues, '4').single.number, 4);
  });
}
