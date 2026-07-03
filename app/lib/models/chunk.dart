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
  int get total => input + output + cacheRead + cacheCreation;

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

enum ItemKind { thinking, text, tool, subagent, unknown }

ItemKind itemKindFromWire(String? s) {
  switch (s) {
    case 'thinking':
      return ItemKind.thinking;
    case 'text':
      return ItemKind.text;
    case 'tool':
      return ItemKind.tool;
    case 'subagent':
      return ItemKind.subagent;
    default:
      return ItemKind.unknown;
  }
}

class Item {
  final String id;
  final ItemKind kind;
  final String? text;
  final bool signature;
  final String? toolName;
  final String? toolId;
  final String? toolInput;
  final String? inputPreview;
  final String? result;
  final bool resultIsError;
  final String? subagentType;
  final String? subagentDesc;
  final String? agentId;
  final bool hasTrace;
  final List<Chunk> trace;

  const Item({
    required this.id,
    required this.kind,
    this.text,
    this.signature = false,
    this.toolName,
    this.toolId,
    this.toolInput,
    this.inputPreview,
    this.result,
    this.resultIsError = false,
    this.subagentType,
    this.subagentDesc,
    this.agentId,
    this.hasTrace = false,
    this.trace = const [],
  });

  factory Item.fromJson(Map<String, dynamic> j) => Item(
        id: j['id'] as String? ?? '',
        kind: itemKindFromWire(j['kind'] as String?),
        text: j['text'] as String?,
        signature: j['signature'] as bool? ?? false,
        toolName: j['toolName'] as String?,
        toolId: j['toolId'] as String?,
        toolInput: j['toolInput'] as String?,
        inputPreview: j['inputPreview'] as String?,
        result: j['result'] as String?,
        resultIsError: j['resultIsError'] as bool? ?? false,
        subagentType: j['subagentType'] as String?,
        subagentDesc: j['subagentDesc'] as String?,
        agentId: j['agentId'] as String?,
        hasTrace: j['hasTrace'] as bool? ?? false,
        trace: (j['trace'] as List?)
                ?.map((e) => Chunk.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
      );

  /// Returns a copy with the heavy tool body filled in. Transcript chunks ship
  /// without [toolInput]/[result] (see the server's Item.MarshalJSON); the
  /// detail view fetches them on demand and fills the item here for rendering.
  Item withToolBody(ToolDetail d) => Item(
        id: id,
        kind: kind,
        text: text,
        signature: signature,
        toolName: toolName,
        toolId: toolId,
        toolInput: d.toolInput,
        inputPreview: inputPreview,
        result: d.result,
        resultIsError: d.resultIsError,
        subagentType: subagentType,
        subagentDesc: subagentDesc,
        agentId: agentId,
        hasTrace: hasTrace,
        trace: trace,
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

enum ChunkKind { user, ai, system, compact, unknown }

ChunkKind chunkKindFromWire(String? s) {
  switch (s) {
    case 'user':
      return ChunkKind.user;
    case 'ai':
      return ChunkKind.ai;
    case 'system':
      return ChunkKind.system;
    case 'compact':
      return ChunkKind.compact;
    default:
      return ChunkKind.unknown;
  }
}

class Chunk {
  final String id;
  final ChunkKind kind;
  final String? timestamp;
  final String? text;
  final String? model;
  final List<Item> items;
  final int thinking;
  final int toolCount;
  final Usage usage;
  final String? stopReason;
  final int durationMs;
  final bool hasContext;
  final double contextPct;
  final double contextFirstPct;
  final int contextDeltaTokens;
  final String? summary;
  final String? label;
  final String? detail;
  final bool isError;
  final String previewItemId;

  const Chunk({
    required this.id,
    required this.kind,
    this.timestamp,
    this.text,
    this.model,
    this.items = const [],
    this.thinking = 0,
    this.toolCount = 0,
    this.usage = const Usage(),
    this.stopReason,
    this.durationMs = 0,
    this.hasContext = false,
    this.contextPct = 0,
    this.contextFirstPct = 0,
    this.contextDeltaTokens = 0,
    this.summary,
    this.label,
    this.detail,
    this.isError = false,
    this.previewItemId = '',
  });

  factory Chunk.fromJson(Map<String, dynamic> j) => Chunk(
        id: j['id'] as String? ?? '',
        kind: chunkKindFromWire(j['kind'] as String?),
        timestamp: j['timestamp'] as String?,
        text: j['text'] as String?,
        model: j['model'] as String?,
        items: (j['items'] as List?)
                ?.map((e) => Item.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
        thinking: (j['thinking'] as num?)?.toInt() ?? 0,
        toolCount: (j['toolCount'] as num?)?.toInt() ?? 0,
        usage: Usage.fromJson(j['usage'] as Map<String, dynamic>?),
        stopReason: j['stopReason'] as String?,
        durationMs: (j['durationMs'] as num?)?.toInt() ?? 0,
        hasContext: j['hasContext'] as bool? ?? false,
        contextPct: (j['contextPct'] as num?)?.toDouble() ?? 0,
        contextFirstPct: (j['contextFirstPct'] as num?)?.toDouble() ?? 0,
        contextDeltaTokens: (j['contextDeltaTokens'] as num?)?.toInt() ?? 0,
        summary: j['summary'] as String?,
        label: j['label'] as String?,
        detail: j['detail'] as String?,
        isError: j['isError'] as bool? ?? false,
        previewItemId: j['previewItemId'] as String? ?? '',
      );

  /// The server-chosen collapsed-preview item, or null when none was stamped.
  Item? get previewItem {
    if (previewItemId.isEmpty) return null;
    for (final it in items) {
      if (it.id == previewItemId) return it;
    }
    return null;
  }
}

class TranscriptDelta {
  final String subId;
  final int fromIndex;
  final List<Chunk> chunks;

  const TranscriptDelta({
    required this.subId,
    required this.fromIndex,
    required this.chunks,
  });

  factory TranscriptDelta.fromJson(Map<String, dynamic> j) => TranscriptDelta(
        subId: j['sub_id'] as String? ?? '',
        fromIndex: (j['from_index'] as num?)?.toInt() ?? 0,
        chunks: (j['chunks'] as List?)
                ?.map((e) => Chunk.fromJson(e as Map<String, dynamic>))
                .toList() ??
            const [],
      );
}
