import 'entry.dart';

DateTime _time(Object? v) =>
    (v is String ? DateTime.tryParse(v) : null) ??
    DateTime.fromMillisecondsSinceEpoch(0, isUtc: true);

/// One bot setting as it applies: [value] is the resolved value, [explicit]
/// whether the bot's own file sets it (else it is the default).
class BotSetting {
  const BotSetting(this.value, {this.explicit = false});
  final String value;
  final bool explicit;
}

/// A registered bot. Ids are composite (`node:id`). [status] is
/// needs_you, waiting, working, idle or unreachable.
class Bot {
  const Bot({
    required this.id,
    this.name = '',
    this.dir = '',
    this.description = '',
    this.agent = '',
    this.model = '',
    this.archived = false,
    this.primary = false,
    this.status = 'idle',
    this.nodeId = '',
    this.nodeLabel = '',
    this.settings = const {},
  });

  final String id;
  final String name;
  final String dir;
  final String description;
  final String agent;
  final String model;
  final bool archived;
  final bool primary;
  final String status;
  final String nodeId;
  final String nodeLabel;

  /// notify, proactivity and memory_mode as they apply; empty when the node
  /// does not report them.
  final Map<String, BotSetting> settings;

  bool get unreachable => status == 'unreachable';

  factory Bot.fromJson(Map<String, dynamic> j) => Bot(
    id: j['id'] as String? ?? '',
    name: j['name'] as String? ?? '',
    dir: j['dir'] as String? ?? '',
    description: j['description'] as String? ?? '',
    agent: j['agent'] as String? ?? '',
    model: j['model'] as String? ?? '',
    archived: j['archived'] as bool? ?? false,
    primary: j['primary'] as bool? ?? false,
    status: j['status'] as String? ?? 'idle',
    nodeId: j['node_id'] as String? ?? '',
    nodeLabel: j['node_label'] as String? ?? '',
    settings: {
      if (j['settings'] is Map)
        for (final e in (j['settings'] as Map).entries)
          if (e.value is Map)
            '${e.key}': BotSetting(
              (e.value as Map)['value'] as String? ?? '',
              explicit: (e.value as Map)['explicit'] as bool? ?? false,
            ),
    },
  );
}

/// The merged bot list; [primary] is the primary bot's composite id.
class BotList {
  const BotList({this.bots = const [], this.primary = ''});
  final List<Bot> bots;
  final String primary;

  factory BotList.fromJson(Object? res) {
    final m = res is Map ? res.cast<String, dynamic>() : const <String, dynamic>{};
    return BotList(
      bots: [
        for (final a in (m['bots'] as List?) ?? const [])
          if (a is Map<String, dynamic>) Bot.fromJson(a),
      ],
      primary: m['primary'] as String? ?? '',
    );
  }
}

/// A conversation or run of a bot. [kind] is chat or run; [startedBy] is
/// you, routine, check-back, event or handoff; [status] is active or closed.
class BotThread {
  const BotThread({
    required this.id,
    this.botId = '',
    this.kind = 'chat',
    this.startedBy = 'you',
    this.title = '',
    this.status = 'active',
    this.unread = false,
    required this.lastActiveAt,
    this.lastActiveNanos = 0,
    this.sessionId = '',
    this.sessionStatus = '',
  });

  final String id;
  final String botId;
  final String kind;
  final String startedBy;
  final String title;
  final String status;
  final bool unread;
  final DateTime lastActiveAt;

  /// [lastActiveAt] in Unix nanoseconds, the unit of the read position, kept
  /// whole: [DateTime] drops the digits below a microsecond.
  final int lastActiveNanos;
  final String sessionId;
  final String sessionStatus;

  factory BotThread.fromJson(Map<String, dynamic> j) => BotThread(
    id: j['id'] as String? ?? '',
    botId: j['bot_id'] as String? ?? '',
    kind: j['kind'] as String? ?? 'chat',
    startedBy: j['started_by'] as String? ?? 'you',
    title: j['title'] as String? ?? '',
    status: j['status'] as String? ?? 'active',
    unread: j['unread'] as bool? ?? false,
    lastActiveAt: _time(j['last_active_at']),
    lastActiveNanos: timestampNanos(j['last_active_at'] as String?),
    sessionId: j['session_id'] as String? ?? '',
    sessionStatus: j['session_status'] as String? ?? '',
  );
}

class BotSendResult {
  const BotSendResult({required this.threadId, this.sessionId = ''});
  final String threadId;
  final String sessionId;

  factory BotSendResult.fromJson(Map<String, dynamic> j) => BotSendResult(
    threadId: j['thread_id'] as String? ?? '',
    sessionId: j['session_id'] as String? ?? '',
  );
}

class BotTimelineSession {
  const BotTimelineSession({
    required this.sessionId,
    this.startIndex = 0,
    this.live = false,
    this.cli = '',
    this.agentSessionId = '',
    this.transcriptPath = '',
  });
  final String sessionId;
  final int startIndex;
  final bool live;
  final String cli;
  final String agentSessionId;
  final String transcriptPath;

  factory BotTimelineSession.fromJson(Map<String, dynamic> j) =>
      BotTimelineSession(
        sessionId: j['session_id'] as String? ?? '',
        startIndex: (j['start_index'] as num?)?.toInt() ?? 0,
        live: j['live'] as bool? ?? false,
        cli: j['cli'] as String? ?? '',
        agentSessionId: j['agent_session_id'] as String? ?? '',
        transcriptPath: j['transcript_path'] as String? ?? '',
      );
}

/// A thread's session transcripts in order with Argus's events spliced in.
class BotTimeline {
  const BotTimeline({
    required this.threadId,
    this.entries = const [],
    this.sessions = const [],
  });
  final String threadId;
  final List<Entry> entries;
  final List<BotTimelineSession> sessions;

  factory BotTimeline.fromJson(Map<String, dynamic> j) => BotTimeline(
    threadId: j['thread_id'] as String? ?? '',
    entries: [
      for (final e in (j['entries'] as List?) ?? const [])
        if (e is Map<String, dynamic>) Entry.fromJson(e),
    ],
    sessions: [
      for (final s in (j['sessions'] as List?) ?? const [])
        if (s is Map<String, dynamic>) BotTimelineSession.fromJson(s),
    ],
  );
}

class BotMemory {
  const BotMemory({
    this.memory = '',
    this.user = '',
    this.profile = '',
    this.topics = const [],
    this.usage = 0,
    this.budget = 0,
  });
  final String memory;
  final String user;
  final String profile;
  final List<String> topics;
  final int usage;
  final int budget;

  factory BotMemory.fromJson(Map<String, dynamic> j) => BotMemory(
    memory: j['memory'] as String? ?? '',
    user: j['user'] as String? ?? '',
    profile: j['profile'] as String? ?? '',
    topics: [for (final t in (j['topics'] as List?) ?? const []) '$t'],
    usage: (j['usage'] as num?)?.toInt() ?? 0,
    budget: (j['budget'] as num?)?.toInt() ?? 0,
  );
}

/// One memory change. [source] is capture, bot, tidy, you or run.
class BotMemoryCommit {
  const BotMemoryCommit({
    required this.hash,
    this.subject = '',
    this.source = '',
    this.threadId = '',
    required this.when,
  });
  final String hash;
  final String subject;
  final String source;
  final String threadId;
  final DateTime when;

  factory BotMemoryCommit.fromJson(Map<String, dynamic> j) =>
      BotMemoryCommit(
        hash: j['hash'] as String? ?? '',
        subject: j['subject'] as String? ?? '',
        source: j['source'] as String? ?? '',
        threadId: j['thread_id'] as String? ?? '',
        when: _time(j['when']),
      );
}

/// One memory edit. [action] is add, update or remove.
class BotMemoryOp {
  const BotMemoryOp({
    required this.action,
    this.file = '',
    this.match = '',
    this.text = '',
    this.until = '',
  });
  final String action;
  final String file;
  final String match;
  final String text;
  final String until;

  factory BotMemoryOp.fromJson(Map<String, dynamic> j) => BotMemoryOp(
    action: j['action'] as String? ?? '',
    file: j['file'] as String? ?? '',
    match: j['match'] as String? ?? '',
    text: j['text'] as String? ?? '',
    until: j['until'] as String? ?? '',
  );
}

/// A staged memory change awaiting accept or reject. [kind] is
/// `instructions` for an instruction rewrite.
class BotMemoryProposal {
  const BotMemoryProposal({
    required this.id,
    this.botId = '',
    this.threadId = '',
    this.source = '',
    this.kind = '',
    this.slug = '',
    this.summary = '',
    this.text = '',
    this.reason = '',
    this.ops = const [],
    this.log = '',
    required this.createdAt,
  });
  final String id;
  final String botId;
  final String threadId;
  final String source;
  final String kind;
  final String slug;
  final String summary;
  final String text;
  final String reason;
  final List<BotMemoryOp> ops;
  final String log;
  final DateTime createdAt;

  bool get isInstructions => kind == 'instructions';
  bool get isRoutine => kind == 'routine';
  bool get isBot => kind == 'bot';

  factory BotMemoryProposal.fromJson(Map<String, dynamic> j) =>
      BotMemoryProposal(
        id: j['id'] as String? ?? '',
        botId: j['bot_id'] as String? ?? '',
        threadId: j['thread_id'] as String? ?? '',
        source: j['source'] as String? ?? '',
        kind: j['kind'] as String? ?? '',
        slug: j['slug'] as String? ?? '',
        summary: j['summary'] as String? ?? '',
        text: j['text'] as String? ?? '',
        reason: j['reason'] as String? ?? '',
        ops: [
          for (final o in (j['ops'] as List?) ?? const [])
            if (o is Map<String, dynamic>) BotMemoryOp.fromJson(o),
        ],
        log: j['log'] as String? ?? '',
        createdAt: _time(j['created_at']),
      );
}

/// The outcome of accepting a proposal.
class BotDecideResult {
  const BotDecideResult({
    this.commit = '',
    this.profileCommit = '',
    this.applied = 0,
    this.rejected = const [],
  });
  final String commit;
  final String profileCommit;
  final int applied;
  final List<String> rejected;

  factory BotDecideResult.fromJson(Map<String, dynamic> j) =>
      BotDecideResult(
        commit: j['commit'] as String? ?? '',
        profileCommit: j['profile_commit'] as String? ?? '',
        applied: (j['applied'] as num?)?.toInt() ?? 0,
        rejected: [for (final r in (j['rejected'] as List?) ?? const []) '$r'],
      );
}

/// The result of registering an existing directory. When [foundInstructions]
/// is set and import was not requested, nothing was registered.
class BotAddResult {
  const BotAddResult({required this.bot, this.foundInstructions = false});
  final Bot bot;
  final bool foundInstructions;

  factory BotAddResult.fromJson(Map<String, dynamic> j) => BotAddResult(
    bot: Bot.fromJson(
      (j['bot'] as Map?)?.cast<String, dynamic>() ?? const {},
    ),
    foundInstructions: j['found_instructions'] as bool? ?? false,
  );
}

/// The commit of a memory or routine change that an argus entry reports, or ''
/// when it is not one.
String undoCommit(Entry e) {
  final kind = e.event?.kind;
  if (e.kind != EntryKind.argus ||
      (kind != 'memory.changed' && kind != 'routine.changed')) {
    return '';
  }
  final p = e.event?.payload;
  return p is Map ? (p['commit'] as String? ?? '') : '';
}

/// The commits a timeline shows as undone.
Set<String> undoneCommits(Iterable<Entry> entries) {
  final out = <String>{};
  for (final e in entries) {
    if (e.kind != EntryKind.argus || e.event?.kind != 'memory.undone') continue;
    final p = e.event?.payload;
    final c = p is Map ? p['commit'] : null;
    if (c is String && c.isNotEmpty) out.add(c);
  }
  return out;
}

/// Whether [commit] (a hash or a prefix of one) is in [undone].
bool commitUndone(Set<String> undone, String commit) =>
    undone.any((c) => c.startsWith(commit) || commit.startsWith(c));

/// A timestamp as Unix nanoseconds, the unit of a thread's read position; 0
/// when it does not parse.
int timestampNanos(String? ts) {
  if (ts == null) return 0;
  final m = RegExp(r'^(.*?)(?:\.(\d+))?(Z|[+-]\d\d:\d\d)$').firstMatch(ts);
  if (m == null) return 0;
  final base = DateTime.tryParse('${m[1]}${m[3]}');
  if (base == null) return 0;
  final frac = (m[2] ?? '').padRight(9, '0').substring(0, 9);
  return base.millisecondsSinceEpoch ~/ 1000 * 1000000000 + int.parse(frac);
}

/// The last entry timestamp of a timeline in Unix nanoseconds, or 0.
int lastEntryNanos(List<Entry> entries) {
  for (var i = entries.length - 1; i >= 0; i--) {
    final n = timestampNanos(entries[i].timestamp);
    if (n > 0) return n;
  }
  return 0;
}

/// The id of the timeline's last live session, or ''.
String liveSessionOf(BotTimeline t) {
  for (var i = t.sessions.length - 1; i >= 0; i--) {
    if (t.sessions[i].live) return t.sessions[i].sessionId;
  }
  return '';
}

/// A routine's budget, or its use; 0 in a budget is unlimited.
class BotRoutineBudget {
  const BotRoutineBudget({this.sessions = 0, this.cost = 0, this.tokens = 0});
  final int sessions;
  final double cost;
  final int tokens;

  factory BotRoutineBudget.fromJson(Object? j) {
    final m = j is Map ? j : const {};
    return BotRoutineBudget(
      sessions: (m['sessions'] as num?)?.toInt() ?? 0,
      cost: (m['cost'] as num?)?.toDouble() ?? 0,
      tokens: (m['tokens'] as num?)?.toInt() ?? 0,
    );
  }
}

/// A routine with its runtime state. [state] is active, paused, disabled,
/// completed or invalid.
class BotRoutine {
  const BotRoutine({
    required this.slug,
    this.botId = '',
    this.what = '',
    this.starts = const [],
    this.state = 'active',
    this.pausedReason = '',
    this.nextDue,
    this.lastRunAt,
    this.lastResult = '',
    this.consecutiveFailures = 0,
    this.budget = const BotRoutineBudget(),
    this.used = const BotRoutineBudget(),
    this.error = '',
  });

  final String slug;
  final String botId;
  final String what;
  final List<String> starts;
  final String state;
  final String pausedReason;
  final DateTime? nextDue;
  final DateTime? lastRunAt;
  final String lastResult;
  final int consecutiveFailures;
  final BotRoutineBudget budget;
  final BotRoutineBudget used;
  final String error;

  factory BotRoutine.fromJson(Map<String, dynamic> j) => BotRoutine(
    slug: j['slug'] as String? ?? '',
    botId: j['bot_id'] as String? ?? '',
    what: j['what'] as String? ?? '',
    starts: [for (final s in (j['starts'] as List?) ?? const []) '$s'],
    state: j['state'] as String? ?? 'active',
    pausedReason: j['paused_reason'] as String? ?? '',
    nextDue: DateTime.tryParse(j['next_due'] as String? ?? ''),
    lastRunAt: DateTime.tryParse(j['last_run_at'] as String? ?? ''),
    lastResult: j['last_result'] as String? ?? '',
    consecutiveFailures: (j['consecutive_failures'] as num?)?.toInt() ?? 0,
    budget: BotRoutineBudget.fromJson(j['budget']),
    used: BotRoutineBudget.fromJson(j['used']),
    error: j['error'] as String? ?? '',
  );
}

/// The usage of the work one kind of starter began.
class BotUsageRow {
  const BotUsageRow({
    this.startedBy = '',
    this.sessions = 0,
    this.cost = 0,
    this.tokens = 0,
  });
  final String startedBy;
  final int sessions;
  final double cost;
  final int tokens;

  factory BotUsageRow.fromJson(Object? j) {
    final m = j is Map ? j : const {};
    return BotUsageRow(
      startedBy: m['started_by'] as String? ?? '',
      sessions: (m['sessions'] as num?)?.toInt() ?? 0,
      cost: (m['cost'] as num?)?.toDouble() ?? 0,
      tokens: (m['tokens'] as num?)?.toInt() ?? 0,
    );
  }
}

/// A bot's usage by starter, and in total. Cost and tokens count only what
/// the bot's CLI reports.
class BotUsage {
  const BotUsage({this.rows = const [], this.total = const BotUsageRow()});
  final List<BotUsageRow> rows;
  final BotUsageRow total;

  factory BotUsage.fromJson(Map<String, dynamic> j) => BotUsage(
    rows: [
      for (final r in (j['rows'] as List?) ?? const []) BotUsageRow.fromJson(r),
    ],
    total: BotUsageRow.fromJson(j['total']),
  );
}

/// The id of the offer an argus entry carries, or '' when it is not one.
String offerIdOf(Entry e) {
  if (e.kind != EntryKind.argus || e.event?.kind != 'offer') return '';
  final p = e.event?.payload;
  return p is Map ? (p['offer_id'] as String? ?? '') : '';
}

/// The ids of the offers a timeline shows as started or dismissed.
Set<String> decidedOffers(Iterable<Entry> entries) {
  final out = <String>{};
  for (final e in entries) {
    final k = e.event?.kind;
    if (e.kind != EntryKind.argus || (k != 'offer.started' && k != 'offer.dismissed')) {
      continue;
    }
    final p = e.event?.payload;
    final id = p is Map ? p['offer_id'] : null;
    if (id is String && id.isNotEmpty) out.add(id);
  }
  return out;
}

/// What an argus entry says: the node's text, else the event's kind.
String argusRowText(Entry e) {
  final detail = e.detail ?? '';
  return detail.isNotEmpty ? detail : (e.event?.kind ?? e.label ?? '');
}

/// The answering bot's name and the answer of a `handoff.answered` entry, or
/// null for any other entry.
({String bot, String text})? handoffAnswerOf(Entry e) {
  if (e.kind != EntryKind.argus || e.event?.kind != 'handoff.answered') {
    return null;
  }
  final p = e.event?.payload is Map ? e.event!.payload as Map : const {};
  final bot = p['bot'] is String ? (p['bot'] as String).trim() : '';
  final text = p['text'] is String ? p['text'] as String : '';
  return (bot: bot.isEmpty ? 'Bot' : bot, text: text);
}

/// The id of the draft event an argus entry carries, or 0 when it is not one.
int draftIdOf(Entry e) =>
    e.kind == EntryKind.argus && e.event?.kind == 'draft' ? e.event!.id : 0;

/// The ids of the draft events a timeline shows as sent or discarded.
Set<int> decidedDrafts(Iterable<Entry> entries) {
  final out = <int>{};
  for (final e in entries) {
    final k = e.event?.kind;
    if (e.kind != EntryKind.argus || (k != 'draft.sent' && k != 'draft.discarded')) {
      continue;
    }
    final p = e.event?.payload;
    final id = p is Map ? p['event_id'] : null;
    if (id is num) out.add(id.toInt());
  }
  return out;
}

/// One approval decision on a bot's action. [decidedBy] is floor, policy,
/// hook, rule, grant, default, user or expiry.
class BotActivity {
  const BotActivity({
    required this.id,
    this.threadId = '',
    this.sessionId = '',
    this.startedBy = '',
    this.tool = '',
    this.target = '',
    this.decision = '',
    this.decidedBy = '',
    this.reason = '',
    required this.createdAt,
  });
  final int id;
  final String threadId;
  final String sessionId;
  final String startedBy;
  final String tool;
  final String target;
  final String decision;
  final String decidedBy;
  final String reason;
  final DateTime createdAt;

  factory BotActivity.fromJson(Map<String, dynamic> j) => BotActivity(
    id: (j['id'] as num?)?.toInt() ?? 0,
    threadId: j['thread_id'] as String? ?? '',
    sessionId: j['session_id'] as String? ?? '',
    startedBy: j['started_by'] as String? ?? '',
    tool: j['tool'] as String? ?? '',
    target: j['target'] as String? ?? '',
    decision: j['decision'] as String? ?? '',
    decidedBy: j['decided_by'] as String? ?? '',
    reason: j['reason'] as String? ?? '',
    createdAt: _time(j['created_at']),
  );
}
