class DirEntry {
  const DirEntry({
    required this.name,
    required this.path,
    this.isDir = false,
    this.symlink = false,
    this.target = '',
  });

  final String name;
  final String path; // repo-relative slash path
  final bool isDir; // for a symlink: its target is a directory in the repo
  final bool symlink;
  final String target; // symlink only: the link text

  factory DirEntry.fromJson(Map<String, dynamic> j) => DirEntry(
    name: j['name'] as String? ?? '',
    path: j['path'] as String? ?? '',
    isDir: j['is_dir'] as bool? ?? false,
    symlink: j['symlink'] as bool? ?? false,
    target: j['target'] as String? ?? '',
  );
}

class DirListing {
  const DirListing({this.path = '', this.entries = const []});

  final String path;
  final List<DirEntry> entries;

  factory DirListing.fromJson(Map<String, dynamic> j) => DirListing(
    path: j['path'] as String? ?? '',
    entries: [
      for (final e in j['entries'] as List? ?? const [])
        if (e is Map<String, dynamic>) DirEntry.fromJson(e),
    ],
  );
}

class FileContent {
  const FileContent({
    required this.path,
    this.content = '',
    this.notShown = false,
  });

  final String path;
  final String content;
  final bool notShown; // binary or oversized

  factory FileContent.fromJson(Map<String, dynamic> j) => FileContent(
    path: j['path'] as String? ?? '',
    content: j['content'] as String? ?? '',
    notShown: j['not_shown'] as bool? ?? false,
  );
}

/// A file's unified diff text from workspace.diff.
class WorkspaceDiff {
  const WorkspaceDiff({
    required this.path,
    this.diff = '',
    this.notShown = false,
  });

  final String path;
  final String diff;
  final bool notShown; // binary or oversized

  factory WorkspaceDiff.fromJson(Map<String, dynamic> j) => WorkspaceDiff(
    path: j['path'] as String? ?? '',
    diff: j['diff'] as String? ?? '',
    notShown: j['not_shown'] as bool? ?? false,
  );
}
