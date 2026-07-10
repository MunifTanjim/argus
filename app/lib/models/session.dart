import 'enums.dart';

List<String> _strList(Object? v) =>
    v == null ? const [] : (v as List).map((e) => e as String).toList();

class TmuxLocation {
  final TmuxServerKind server;
  final String paneId;
  final String sessionName;
  final int windowIndex;
  final String currentPath;

  const TmuxLocation({
    required this.server,
    required this.paneId,
    required this.sessionName,
    required this.windowIndex,
    required this.currentPath,
  });

  factory TmuxLocation.fromJson(Map<String, dynamic> j) => TmuxLocation(
        server: tmuxServerFromWire(j['server'] as String?),
        paneId: j['pane_id'] as String? ?? '',
        sessionName: j['session_name'] as String? ?? '',
        windowIndex: (j['window_index'] as num?)?.toInt() ?? 0,
        currentPath: j['current_path'] as String? ?? '',
      );
}

class Summary {
  final String? modelName;
  final String? modelColor;
  final bool hasContext;
  final double contextPct;
  final int tokens;
  final String? task;
  final String? lastActivity;

  const Summary({
    this.modelName,
    this.modelColor,
    this.hasContext = false,
    this.contextPct = 0,
    this.tokens = 0,
    this.task,
    this.lastActivity,
  });

  factory Summary.fromJson(Map<String, dynamic> j) => Summary(
        modelName: j['model_name'] as String?,
        modelColor: j['model_color'] as String?,
        hasContext: j['has_context'] as bool? ?? false,
        contextPct: (j['context_pct'] as num?)?.toDouble() ?? 0,
        tokens: (j['tokens'] as num?)?.toInt() ?? 0,
        task: j['task'] as String?,
        lastActivity: j['last_activity'] as String?,
      );
}

class QuestionSpec {
  final String? header;
  final String? question;
  final bool multiSelect;
  final List<String> options;
  final List<String> optionDescriptions;
  final List<String> optionPreviews;

  const QuestionSpec({
    this.header,
    this.question,
    this.multiSelect = false,
    this.options = const [],
    this.optionDescriptions = const [],
    this.optionPreviews = const [],
  });

  factory QuestionSpec.fromJson(Map<String, dynamic> j) => QuestionSpec(
        header: j['header'] as String?,
        question: j['question'] as String?,
        multiSelect: j['multi_select'] as bool? ?? false,
        options: _strList(j['options']),
        optionDescriptions: _strList(j['option_descriptions']),
        optionPreviews: _strList(j['option_previews']),
      );
}

/// A server-built decision choice (e.g. ExitPlanMode approve variants). The
/// client renders [label] and echoes [value] back; [reject] marks the deny
/// choice, which opens a free-text field prompted by [placeholder].
class DecisionOption {
  final String label;
  final String value;
  final bool reject;
  final String placeholder;

  const DecisionOption({
    required this.label,
    required this.value,
    this.reject = false,
    this.placeholder = '',
  });

  factory DecisionOption.fromJson(Map<String, dynamic> j) => DecisionOption(
        label: j['label'] as String? ?? '',
        value: j['value'] as String? ?? '',
        reject: j['reject'] as bool? ?? false,
        placeholder: j['placeholder'] as String? ?? '',
      );
}

class Interaction {
  final InteractionKind kind;
  final String? message;
  final String? toolName;
  final String? toolInput;
  final List<QuestionSpec> questions;
  final String? plan;
  final List<DecisionOption> options;

  const Interaction({
    required this.kind,
    this.message,
    this.toolName,
    this.toolInput,
    this.questions = const [],
    this.plan,
    this.options = const [],
  });

  factory Interaction.fromJson(Map<String, dynamic> j) => Interaction(
        kind: interactionKindFromWire(j['kind'] as String?),
        message: j['message'] as String?,
        toolName: j['tool_name'] as String?,
        toolInput: j['tool_input'] as String?,
        questions: (j['questions'] as List?)
                ?.map((e) => QuestionSpec.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
        plan: j['plan'] as String?,
        options: (j['options'] as List?)
                ?.map((e) => DecisionOption.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
      );
}

class Session {
  final String id;
  final String agent;
  final String? agentSessionId;
  final String? name;
  final TmuxLocation tmux;
  final String? cwd;
  final String? transcriptPath;
  final SessionStatus status;
  final String statusLabel;
  final SessionSource source;
  final FrontendKind frontend;
  final String? repo;
  final Summary? summary;
  final Interaction? interaction;
  final String? nodeId;
  final String? nodeLabel;
  final bool offline;

  const Session({
    required this.id,
    required this.agent,
    required this.tmux,
    required this.status,
    this.statusLabel = '',
    required this.source,
    this.frontend = FrontendKind.unknown,
    this.agentSessionId,
    this.name,
    this.cwd,
    this.transcriptPath,
    this.repo,
    this.summary,
    this.interaction,
    this.nodeId,
    this.nodeLabel,
    this.offline = false,
  });

  factory Session.fromJson(Map<String, dynamic> j) => Session(
        id: j['id'] as String,
        agent: j['agent'] as String? ?? '',
        agentSessionId: j['agent_session_id'] as String?,
        name: j['name'] as String?,
        tmux: TmuxLocation.fromJson(j['tmux'] as Map<String, dynamic>),
        cwd: j['cwd'] as String?,
        transcriptPath: j['transcript_path'] as String?,
        status: statusFromWire(j['status'] as String?),
        statusLabel: j['status_label'] as String? ?? '',
        source: sourceFromWire(j['source'] as String?),
        frontend: frontendFromWire(j['frontend'] as String?),
        repo: j['repo'] as String?,
        summary: j['summary'] == null
            ? null
            : Summary.fromJson(j['summary'] as Map<String, dynamic>),
        interaction: j['interaction'] == null
            ? null
            : Interaction.fromJson(j['interaction'] as Map<String, dynamic>),
        nodeId: j['node_id'] as String?,
        nodeLabel: j['node_label'] as String?,
        offline: j['offline'] as bool? ?? false,
      );

  /// The label shown wherever a session needs a title: repo, else name, else id.
  String get displayTitle => (repo?.isNotEmpty ?? false)
      ? repo!
      : (name?.isNotEmpty ?? false)
          ? name!
          : id;

  /// The Claude session name, shown as a secondary label beside [displayTitle].
  /// Null when it would duplicate the primary title (e.g. repo is empty, so the
  /// name is already the title).
  String? get displayName =>
      (name?.isNotEmpty ?? false) && name != displayTitle ? name : null;

  /// Whether argus can drive this session's terminal. Derived from frontend:
  /// only tmux sessions are controllable; vscode/external are decision-only.
  bool get controllable => frontend == FrontendKind.tmux;
}
