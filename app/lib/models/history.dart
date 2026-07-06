class HistoryProject {
  final String projectDir;
  final String cwd;
  final String? repo;
  final String label;
  final int sessionCount;
  final String lastActivity;
  final String? nodeId;
  final String? nodeLabel;

  const HistoryProject({
    required this.projectDir,
    required this.cwd,
    this.repo,
    required this.label,
    required this.sessionCount,
    required this.lastActivity,
    this.nodeId,
    this.nodeLabel,
  });

  factory HistoryProject.fromJson(Map<String, dynamic> j) => HistoryProject(
        projectDir: j['project_dir'] as String? ?? '',
        cwd: j['cwd'] as String? ?? '',
        repo: j['repo'] as String?,
        label: j['label'] as String? ?? '',
        sessionCount: (j['session_count'] as num?)?.toInt() ?? 0,
        lastActivity: j['last_activity'] as String? ?? '',
        nodeId: j['node_id'] as String?,
        nodeLabel: j['node_label'] as String?,
      );
}

class HistorySession {
  final String sessionId;
  final String? title;
  final String? firstMessage;
  final String transcriptPath;
  final String? modelName;
  final String? modelColor;
  final String lastActivity;
  final int tokens;
  final int turnCount;
  final int durationMs;
  final String? nodeId;
  final String? nodeLabel;
  final String agent;

  const HistorySession({
    required this.sessionId,
    this.title,
    this.firstMessage,
    required this.transcriptPath,
    this.modelName,
    this.modelColor,
    required this.lastActivity,
    required this.tokens,
    required this.turnCount,
    required this.durationMs,
    this.nodeId,
    this.nodeLabel,
    this.agent = '',
  });

  factory HistorySession.fromJson(Map<String, dynamic> j) => HistorySession(
        sessionId: j['session_id'] as String? ?? '',
        title: j['title'] as String?,
        firstMessage: j['first_message'] as String?,
        transcriptPath: j['transcript_path'] as String? ?? '',
        modelName: j['model_name'] as String?,
        modelColor: j['model_color'] as String?,
        lastActivity: j['last_activity'] as String? ?? '',
        tokens: (j['tokens'] as num?)?.toInt() ?? 0,
        turnCount: (j['turn_count'] as num?)?.toInt() ?? 0,
        durationMs: (j['duration_ms'] as num?)?.toInt() ?? 0,
        nodeId: j['node_id'] as String?,
        nodeLabel: j['node_label'] as String?,
        agent: j['agent'] as String? ?? '',
      );

  /// The label shown wherever a history session needs a title: title, else the
  /// first message, else the session id.
  String get displayTitle => (title?.isNotEmpty ?? false)
      ? title!
      : (firstMessage?.isNotEmpty ?? false)
          ? firstMessage!
          : sessionId;
}

class HistorySessionPage {
  final List<HistorySession> items;
  final bool hasMore;

  const HistorySessionPage({
    required this.items,
    required this.hasMore,
  });

  factory HistorySessionPage.fromJson(Map<String, dynamic> j) =>
      HistorySessionPage(
        items: (j['items'] as List?)
                ?.map((e) => HistorySession.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
        hasMore: j['has_more'] as bool? ?? false,
      );
}
