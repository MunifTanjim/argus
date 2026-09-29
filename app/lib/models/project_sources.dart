class BranchInfo {
  const BranchInfo({
    required this.name,
    this.remote = false,
    this.local = false,
    this.checkedOut = false,
  });

  final String name;
  final bool remote;
  final bool local;
  final bool checkedOut; // a worktree has it checked out

  factory BranchInfo.fromJson(Map<String, dynamic> j) => BranchInfo(
    name: j['name'] as String? ?? '',
    remote: j['remote'] as bool? ?? false,
    local: j['local'] as bool? ?? false,
    checkedOut: j['checked_out'] as bool? ?? false,
  );
}

class PrInfo {
  const PrInfo({
    required this.number,
    required this.title,
    this.author = '',
    this.headBranch = '',
    this.baseBranch = '',
  });

  final int number;
  final String title;
  final String author;
  final String headBranch;
  final String baseBranch;

  factory PrInfo.fromJson(Map<String, dynamic> j) => PrInfo(
    number: (j['number'] as num?)?.toInt() ?? 0,
    title: j['title'] as String? ?? '',
    author: j['author'] as String? ?? '',
    headBranch: j['head_branch'] as String? ?? '',
    baseBranch: j['base_branch'] as String? ?? '',
  );
}

class IssueInfo {
  const IssueInfo({
    required this.number,
    required this.title,
    this.author = '',
  });

  final int number;
  final String title;
  final String author;

  factory IssueInfo.fromJson(Map<String, dynamic> j) => IssueInfo(
    number: (j['number'] as num?)?.toInt() ?? 0,
    title: j['title'] as String? ?? '',
    author: j['author'] as String? ?? '',
  );
}

/// A forge list; [truncated] means the forge's list limit cut it off.
class SourceList<T> {
  const SourceList(this.items, {this.truncated = false});

  final List<T> items;
  final bool truncated;
}

List<T> _items<T>(
  Object? res,
  String key,
  T Function(Map<String, dynamic>) parse,
) {
  final list = res is Map ? res[key] : null;
  if (list is! List) return const [];
  return [
    for (final e in list)
      if (e is Map<String, dynamic>) parse(e),
  ];
}

bool _truncated(Object? res) => res is Map && res['truncated'] == true;

List<BranchInfo> parseBranches(Object? res) =>
    _items(res, 'branches', BranchInfo.fromJson);

SourceList<PrInfo> parsePrs(Object? res) =>
    SourceList(_items(res, 'prs', PrInfo.fromJson), truncated: _truncated(res));

SourceList<IssueInfo> parseIssues(Object? res) => SourceList(
  _items(res, 'issues', IssueInfo.fromJson),
  truncated: _truncated(res),
);

class CreateRequest {
  const CreateRequest({
    required this.source,
    this.branch = '',
    this.number = 0,
    this.targetBranch = '',
  });

  final String source; // new|branch|pr|issue
  final String branch;
  final int number;
  final String targetBranch; // '' = the project default branch

  Map<String, dynamic> params(String projectId) => {
    'project_id': projectId,
    'source': source,
    if (branch.isNotEmpty) 'branch': branch,
    if (number > 0) 'number': number,
    if (targetBranch.isNotEmpty && source != 'pr')
      'target_branch': targetBranch,
  };
}

class CreateResult {
  const CreateResult({
    required this.workspaceId,
    required this.dir,
    this.warning = '',
    this.prompt = '',
    this.setup = '',
  });

  final String workspaceId;
  final String dir;
  final String warning;
  final String prompt; // an issue's text, for an optional agent spawn
  final String setup; // the setup command that started in the background

  factory CreateResult.fromJson(Map<String, dynamic> j) => CreateResult(
    workspaceId: j['workspace_id'] as String? ?? '',
    dir: j['dir'] as String? ?? '',
    warning: j['warning'] as String? ?? '',
    prompt: j['prompt'] as String? ?? '',
    setup: j['setup'] as String? ?? '',
  );
}

String _q(String q) => q.trim().toLowerCase();

List<BranchInfo> filterBranches(List<BranchInfo> all, String q) {
  final s = _q(q);
  return [
    for (final b in all)
      if (s.isEmpty || b.name.toLowerCase().contains(s)) b,
  ];
}

List<PrInfo> filterPrs(List<PrInfo> all, String q) {
  final s = _q(q);
  return [
    for (final p in all)
      if (s.isEmpty ||
          '${p.number} ${p.title} ${p.author} ${p.headBranch} ${p.baseBranch}'
              .toLowerCase()
              .contains(s))
        p,
  ];
}

List<IssueInfo> filterIssues(List<IssueInfo> all, String q) {
  final s = _q(q);
  return [
    for (final i in all)
      if (s.isEmpty ||
          '${i.number} ${i.title} ${i.author}'.toLowerCase().contains(s))
        i,
  ];
}
