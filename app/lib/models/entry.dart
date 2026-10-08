class Usage {
  final int input;
  final int output;
  final int cacheRead;
  final int cacheCreation;

  const Usage({
    this.input = 0,
    this.output = 0,
    this.cacheRead = 0,
    this.cacheCreation = 0,
  });

  int get context => input + cacheRead + cacheCreation;

  factory Usage.fromJson(Map<String, dynamic>? j) {
    if (j == null) return const Usage();
    int n(String k) => (j[k] as num?)?.toInt() ?? 0;
    return Usage(
      input: n('input'),
      output: n('output'),
      cacheRead: n('cacheRead'),
      cacheCreation: n('cacheCreation'),
    );
  }
}

enum EntryKind {
  user,
  thinking,
  text,
  tool,
  subagent,
  skill,
  turnEnd,
  system,
  compact,
  shell,
  unknown,
}

EntryKind entryKindFromWire(String? s) {
  switch (s) {
    case 'user':
      return EntryKind.user;
    case 'thinking':
      return EntryKind.thinking;
    case 'text':
      return EntryKind.text;
    case 'tool':
      return EntryKind.tool;
    case 'subagent':
      return EntryKind.subagent;
    case 'skill':
      return EntryKind.skill;
    case 'turn_end':
      return EntryKind.turnEnd;
    case 'system':
      return EntryKind.system;
    case 'compact':
      return EntryKind.compact;
    case 'shell':
      return EntryKind.shell;
    default:
      return EntryKind.unknown;
  }
}

/// Teammates are also modeled as subagents (with [isTeammate] set).
class Subagent {
  final String id;
  final String name;
  final String type;
  final String desc;
  final String status;
  final String color;
  final bool isTeammate;
  final bool idle;
  final bool hasTrace;
  final List<Entry> trace;

  const Subagent({
    this.id = '',
    this.name = '',
    this.type = '',
    this.desc = '',
    this.status = '',
    this.color = '',
    this.isTeammate = false,
    this.idle = false,
    this.hasTrace = false,
    this.trace = const [],
  });

  factory Subagent.fromJson(Map<String, dynamic> j) => Subagent(
        id: j['id'] as String? ?? '',
        name: j['name'] as String? ?? '',
        type: j['type'] as String? ?? '',
        desc: j['desc'] as String? ?? '',
        status: j['status'] as String? ?? '',
        color: j['color'] as String? ?? '',
        isTeammate: j['isTeammate'] as bool? ?? false,
        idle: j['idle'] as bool? ?? false,
        hasTrace: j['hasTrace'] as bool? ?? false,
        trace: (j['trace'] as List?)
                ?.map((e) => Entry.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
      );
}

/// One unit of the flat transcript timeline. Fields are used per [kind].
class Entry {
  final String id;
  final EntryKind kind;
  final String? timestamp;
  final String? text;
  final bool signature;
  final String? toolName;
  final String? toolId;
  final String? toolInput;
  final String? inputPreview;
  final String? result;
  final bool resultIsError;
  final List<Subagent> subagents;
  // turn_end
  final String? modelName;
  final String? modelColor;
  final Usage usage;
  final String? stopReason;
  final int durationMs;
  final int thinking;
  final int toolCount;
  final bool interrupted;
  final bool hasContext;
  final double contextPct;
  final double contextFirstPct;
  final int contextDeltaTokens;
  // system / compact / shell
  final String? summary;
  final String? label;
  final String? detail;
  final bool isError;

  const Entry({
    required this.id,
    required this.kind,
    this.timestamp,
    this.text,
    this.signature = false,
    this.toolName,
    this.toolId,
    this.toolInput,
    this.inputPreview,
    this.result,
    this.resultIsError = false,
    this.subagents = const [],
    this.modelName,
    this.modelColor,
    this.usage = const Usage(),
    this.stopReason,
    this.durationMs = 0,
    this.thinking = 0,
    this.toolCount = 0,
    this.interrupted = false,
    this.hasContext = false,
    this.contextPct = 0,
    this.contextFirstPct = 0,
    this.contextDeltaTokens = 0,
    this.summary,
    this.label,
    this.detail,
    this.isError = false,
  });

  Subagent? get soleSubagent => subagents.length == 1 ? subagents.first : null;

  bool get isTeammate => soleSubagent?.isTeammate ?? false;

  bool get isToolCall =>
      kind == EntryKind.tool ||
      kind == EntryKind.skill ||
      (kind == EntryKind.subagent && !isTeammate);

  factory Entry.fromJson(Map<String, dynamic> j) => Entry(
        id: j['id'] as String? ?? '',
        kind: entryKindFromWire(j['kind'] as String?),
        timestamp: j['timestamp'] as String?,
        text: j['text'] as String?,
        signature: j['signature'] as bool? ?? false,
        toolName: j['toolName'] as String?,
        toolId: j['toolId'] as String?,
        toolInput: j['toolInput'] as String?,
        inputPreview: j['inputPreview'] as String?,
        result: j['result'] as String?,
        resultIsError: j['resultIsError'] as bool? ?? false,
        subagents: (j['subagents'] as List?)
                ?.map((e) => Subagent.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
        modelName: j['modelName'] as String?,
        modelColor: j['modelColor'] as String?,
        usage: Usage.fromJson(j['usage'] as Map<String, dynamic>?),
        stopReason: j['stopReason'] as String?,
        durationMs: (j['durationMs'] as num?)?.toInt() ?? 0,
        thinking: (j['thinking'] as num?)?.toInt() ?? 0,
        toolCount: (j['toolCount'] as num?)?.toInt() ?? 0,
        interrupted: j['interrupted'] as bool? ?? false,
        hasContext: j['hasContext'] as bool? ?? false,
        contextPct: (j['contextPct'] as num?)?.toDouble() ?? 0,
        contextFirstPct: (j['contextFirstPct'] as num?)?.toDouble() ?? 0,
        contextDeltaTokens: (j['contextDeltaTokens'] as num?)?.toInt() ?? 0,
        summary: j['summary'] as String?,
        label: j['label'] as String?,
        detail: j['detail'] as String?,
        isError: j['isError'] as bool? ?? false,
      );

  /// Entries ship without [toolInput]/[result] (see the server's
  /// Entry.MarshalJSON).
  Entry withToolBody(ToolDetail d) => Entry(
        id: id,
        kind: kind,
        timestamp: timestamp,
        text: text,
        signature: signature,
        toolName: toolName,
        toolId: toolId,
        toolInput: d.toolInput,
        inputPreview: inputPreview,
        result: d.result,
        resultIsError: d.resultIsError,
        subagents: subagents,
      );
}

/// ToolDetail is one tool item's heavy body, fetched on demand via
/// sessions.toolDetail / sessions.historyToolDetail.
class ToolDetail {
  final String? toolInput;
  final String? result;
  final bool resultIsError;

  const ToolDetail({this.toolInput, this.result, this.resultIsError = false});

  factory ToolDetail.fromJson(Map<String, dynamic> j) => ToolDetail(
        toolInput: j['toolInput'] as String?,
        result: j['result'] as String?,
        resultIsError: j['resultIsError'] as bool? ?? false,
      );
}

class TranscriptDelta {
  final String subId;
  final int fromIndex;
  final List<Entry> entries;

  const TranscriptDelta({
    required this.subId,
    required this.fromIndex,
    required this.entries,
  });

  factory TranscriptDelta.fromJson(Map<String, dynamic> j) => TranscriptDelta(
        subId: j['sub_id'] as String? ?? '',
        fromIndex: (j['from_index'] as num?)?.toInt() ?? 0,
        entries: (j['entries'] as List?)
                ?.map((e) => Entry.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
      );
}
