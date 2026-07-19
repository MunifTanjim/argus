class ChangedFile {
  const ChangedFile({
    required this.path,
    required this.change,
    this.origPath,
    this.staged = false,
    this.unstaged = false,
  });

  final String path;
  final String change; // added|modified|deleted|renamed|untracked
  final String? origPath; // rename source (HEAD-side)
  final bool staged; // index differs from HEAD
  final bool unstaged; // working tree differs from index

  factory ChangedFile.fromJson(Map<String, dynamic> j) {
    final origPath = j['orig_path'] as String?;
    return ChangedFile(
      path: j['path'] as String? ?? '',
      change: j['change'] as String? ?? '',
      origPath: (origPath != null && origPath.isNotEmpty) ? origPath : null,
      staged: j['staged'] as bool? ?? false,
      unstaged: j['unstaged'] as bool? ?? false,
    );
  }
}

class FileDiff {
  const FileDiff({
    required this.path,
    this.oldContent = '',
    this.newContent = '',
    this.notShown = false,
  });

  final String path;
  final String oldContent;
  final String newContent;
  final bool notShown;

  factory FileDiff.fromJson(Map<String, dynamic> j) => FileDiff(
        path: j['path'] as String? ?? '',
        oldContent: j['old_content'] as String? ?? '',
        newContent: j['new_content'] as String? ?? '',
        notShown: j['not_shown'] as bool? ?? false,
      );
}

class Commit {
  const Commit({
    required this.sha,
    required this.short,
    required this.subject,
    required this.author,
    required this.unixSec,
  });

  final String sha;
  final String short;
  final String subject;
  final String author;
  final int unixSec; // authored time (epoch seconds)

  factory Commit.fromJson(Map<String, dynamic> j) => Commit(
        sha: j['sha'] as String? ?? '',
        short: j['short'] as String? ?? '',
        subject: j['subject'] as String? ?? '',
        author: j['author'] as String? ?? '',
        unixSec: (j['unix_sec'] as num?)?.toInt() ?? 0,
      );
}

class CommitList {
  const CommitList({this.commits = const [], this.unpushed = false});

  final List<Commit> commits;
  final bool unpushed;

  factory CommitList.fromJson(Map<String, dynamic> j) => CommitList(
        commits: (j['commits'] as List? ?? const [])
            .map((e) => Commit.fromJson(e as Map<String, dynamic>))
            .toList(),
        unpushed: j['unpushed'] as bool? ?? false,
      );
}
