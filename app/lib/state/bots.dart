import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/bot.dart';
import '../transport/gateway_client.dart';
import 'gateway.dart';

/// Wraps the bot RPCs. Ids are composite; the E2E client routes
/// them to their node.
class BotsApi {
  BotsApi(this._clientOf);
  final GatewayClient? Function() _clientOf;

  GatewayClient get _client =>
      _clientOf() ?? (throw StateError('not connected'));

  Future<Object?> _call(String method, Map<String, dynamic> params) =>
      _client.call(method, params);

  Future<Map<String, dynamic>> _map(
    String method,
    Map<String, dynamic> params,
  ) async => ((await _call(method, params)) as Map?)?.cast<String, dynamic>() ??
      const {};

  Future<BotList> list({bool includeArchived = false}) async => BotList
      .fromJson(
        await _call('bot.list', {
          if (includeArchived) 'include_archived': true,
        }),
      );

  Future<Bot> create({
    String? name,
    String? dir,
    String? agent,
    String? model,
    String? from,
    String? nodeId,
  }) async => Bot.fromJson(
    await _map('bot.create', {
      if (name != null) 'name': name,
      if (dir != null) 'dir': dir,
      if (agent != null) 'agent': agent,
      if (model != null) 'model': model,
      if (from != null) 'from': from,
      if (nodeId != null) 'node_id': nodeId,
    }),
  );

  Future<BotAddResult> add(
    String dir, {
    String? name,
    bool importInstructions = false,
    String? nodeId,
  }) async => BotAddResult.fromJson(
    await _map('bot.add', {
      'dir': dir,
      if (name != null) 'name': name,
      if (importInstructions) 'import_instructions': true,
      if (nodeId != null) 'node_id': nodeId,
    }),
  );

  Future<Bot> show(String botId) async =>
      Bot.fromJson(await _map('bot.show', {'bot_id': botId}));

  Future<Bot> rename(String botId, String name) async => Bot.fromJson(
    await _map('bot.rename', {'bot_id': botId, 'name': name}),
  );

  Future<void> forget(String botId, {bool purge = false}) => _call(
    'bot.forget',
    {'bot_id': botId, if (purge) 'purge': true},
  );

  /// Makes [botId] its node's primary bot.
  Future<void> setPrimary(String botId) =>
      _call('bot.setPrimary', {'bot_id': botId});

  /// Unsets the primary bot of [nodeId].
  Future<void> unsetPrimary(String nodeId) =>
      _call('bot.setPrimary', {if (nodeId.isNotEmpty) 'node_id': nodeId});

  Future<BotSendResult> send(
    String botId,
    String text, {
    String? threadId,
    bool newThread = false,
  }) async => BotSendResult.fromJson(
    await _map('bot.thread.send', {
      'bot_id': botId,
      if (threadId != null) 'thread_id': threadId,
      'text': text,
      if (newThread) 'new': true,
    }),
  );

  Future<List<BotThread>> threads(String botId) async {
    final res = await _map('bot.thread.list', {'bot_id': botId});
    return [
      for (final t in (res['threads'] as List?) ?? const [])
        if (t is Map<String, dynamic>) BotThread.fromJson(t),
    ];
  }

  /// [position] is a thread's lastActiveAt in Unix nanoseconds; null means now.
  Future<void> markRead(String threadId, {int? position}) => _call(
    'bot.thread.markRead',
    {'thread_id': threadId, if (position != null) 'position': position},
  );

  Future<BotTimeline> timeline(String threadId) async =>
      BotTimeline.fromJson(
        await _map('bot.thread.timeline', {'thread_id': threadId}),
      );

  Future<BotMemory> memory(String botId) async =>
      BotMemory.fromJson(
        await _map('bot.memory.show', {'bot_id': botId}),
      );

  /// [profile] lists the shared profile's commits instead of the bot's.
  Future<List<BotMemoryCommit>> memoryLog(
    String botId, {
    int? limit,
    bool profile = false,
  }) async {
    final res = await _map('bot.memory.log', {
      'bot_id': botId,
      if (limit != null) 'limit': limit,
      if (profile) 'profile': true,
    });
    return [
      for (final c in (res['commits'] as List?) ?? const [])
        if (c is Map<String, dynamic>) BotMemoryCommit.fromJson(c),
    ];
  }

  /// Reverts [commit] and returns the revert commit's hash.
  Future<String> memoryUndo(
    String botId,
    String commit, {
    bool profile = false,
    String threadId = '',
  }) async {
    final res = await _map('bot.memory.undo', {
      'bot_id': botId,
      'commit': commit,
      if (profile) 'profile': true,
      if (threadId.isNotEmpty) 'thread_id': threadId,
    });
    return res['commit'] as String? ?? '';
  }

  Future<List<BotMemoryProposal>> proposals(String botId) async {
    final res = await _map('bot.memory.proposals', {'bot_id': botId});
    return [
      for (final p in (res['proposals'] as List?) ?? const [])
        if (p is Map<String, dynamic>) BotMemoryProposal.fromJson(p),
    ];
  }

  Future<BotDecideResult> decide(String proposalId, bool accept) async =>
      BotDecideResult.fromJson(
        await _map('bot.memory.decide', {
          'proposal_id': proposalId,
          'accept': accept,
        }),
      );

  Future<List<BotRoutine>> routines(String botId) async {
    final res = await _map('bot.routine.list', {'bot_id': botId});
    return [
      for (final r in (res['routines'] as List?) ?? const [])
        if (r is Map<String, dynamic>) BotRoutine.fromJson(r),
    ];
  }

  /// Starts a test run now and returns its run id.
  Future<String> runRoutine(String botId, String slug) async {
    final res = await _map('bot.routine.run', {
      'bot_id': botId,
      'slug': slug,
    });
    return res['run_id'] as String? ?? '';
  }

  Future<BotRoutine> pauseRoutine(String botId, String slug) async =>
      BotRoutine.fromJson(
        await _map('bot.routine.pause', {'bot_id': botId, 'slug': slug}),
      );

  Future<BotRoutine> resumeRoutine(String botId, String slug) async =>
      BotRoutine.fromJson(
        await _map('bot.routine.resume', {'bot_id': botId, 'slug': slug}),
      );

  /// Usage since [since], or all time when null.
  Future<BotUsage> usage(String botId, {DateTime? since}) async =>
      BotUsage.fromJson(
        await _map('bot.usage', {
          'bot_id': botId,
          if (since != null) 'since': since.toUtc().toIso8601String(),
        }),
      );

  /// Sets one setting (notify, proactivity, memory.mode, ...); an empty
  /// [value] removes it, so the default applies. Returns the commit made.
  Future<String> setSetting(String botId, String key, String value) async {
    final res = await _map('bot.set', {
      'bot_id': botId,
      'key': key,
      'value': value,
    });
    return res['commit'] as String? ?? '';
  }

  /// Runs a drafted action: [eventId] is the id of its draft event.
  Future<void> sendDraft(String threadId, int eventId) => _call(
    'bot.draft.send',
    {'thread_id': threadId, 'event_id': eventId},
  );

  Future<void> discardDraft(String threadId, int eventId) => _call(
    'bot.draft.discard',
    {'thread_id': threadId, 'event_id': eventId},
  );

  /// The bot's approval decisions, newest first; [unattended] keeps only
  /// work the user did not start.
  Future<List<BotActivity>> activity(
    String botId, {
    bool unattended = false,
    int limit = 20,
  }) async {
    final res = await _map('bot.activity', {
      'bot_id': botId,
      if (unattended) 'unattended': true,
      'limit': limit,
    });
    return [
      for (final a in (res['activity'] as List?) ?? const [])
        if (a is Map<String, dynamic>) BotActivity.fromJson(a),
    ];
  }

  /// Sends the offer's prompt to its thread.
  Future<BotSendResult> startOffer(String threadId, String offerId) async =>
      BotSendResult.fromJson(
        await _map('bot.offer.start', {
          'thread_id': threadId,
          'offer_id': offerId,
        }),
      );

  Future<void> dismissOffer(String threadId, String offerId) => _call(
    'bot.offer.dismiss',
    {'thread_id': threadId, 'offer_id': offerId},
  );

  /// Returns how many changes were staged for review.
  Future<int> importMemory(String botId, String content) async {
    final res = await _map('bot.memory.import', {
      'bot_id': botId,
      'content': content,
    });
    return (res['staged'] as num?)?.toInt() ?? 0;
  }
}

/// The connected client, or null. Tests override it.
final botsClientProvider = Provider<GatewayClient?>(
  (ref) => ref.read(gatewayProvider)?.client,
);

final botsApiProvider = Provider<BotsApi>(
  (ref) => BotsApi(() => ref.read(botsClientProvider)),
);

/// The size of a bot's core memory, its budget, and when it last changed
/// (null when unknown).
class BotMemoryUsage {
  const BotMemoryUsage({this.usage = 0, this.budget = 0, this.last});
  final int usage;
  final int budget;
  final DateTime? last;
}

/// The bot's memory usage, or null when the node has no memory to show.
/// Reloads when the bot list changes.
final botMemoryUsageProvider = FutureProvider.autoDispose
    .family<BotMemoryUsage?, String>((ref, botId) async {
      ref.watch(botsProvider.select((s) => s.bots));
      final api = ref.read(botsApiProvider);
      final BotMemory mem;
      try {
        mem = await api.memory(botId);
      } catch (_) {
        return null;
      }
      DateTime? last;
      try {
        final log = await api.memoryLog(botId, limit: 1);
        if (log.isNotEmpty) last = log.first.when;
      } catch (_) {}
      return BotMemoryUsage(usage: mem.usage, budget: mem.budget, last: last);
    });

/// The bot's usage over the last seven days. Reloads when the bot list
/// changes.
final botUsageProvider = FutureProvider.autoDispose
    .family<BotUsage, String>((ref, botId) {
      ref.watch(botsProvider.select((s) => s.bots));
      return ref
          .read(botsApiProvider)
          .usage(botId, since: DateTime.now().subtract(const Duration(days: 7)));
    });

/// The bot's recent approval decisions, all or only unattended work.
final botActivityProvider = FutureProvider.autoDispose
    .family<List<BotActivity>, (String, bool)>((ref, key) {
      ref.watch(botsProvider.select((s) => s.bots));
      return ref.read(botsApiProvider).activity(key.$1, unattended: key.$2);
    });

/// The bot's routines. Reloads when the bot list changes.
final botRoutinesProvider = FutureProvider.autoDispose
    .family<List<BotRoutine>, String>((ref, botId) {
      ref.watch(botsProvider.select((s) => s.bots));
      return ref.read(botsApiProvider).routines(botId);
    });

class BotsState {
  const BotsState({
    this.bots = const [],
    this.threads = const {},
    this.loaded = false,
    this.error,
  });

  final List<Bot> bots;
  // Each reachable bot's threads; a bot not listed has none loaded yet.
  final Map<String, List<BotThread>> threads;
  final bool loaded;
  final String? error; // the last load failed; bots are the last good list

  /// The bot id and thread with the gateway-qualified id [threadId].
  (String, BotThread)? findThread(String threadId) {
    for (final e in threads.entries) {
      for (final t in e.value) {
        if (t.id == threadId) return (e.key, t);
      }
    }
    return null;
  }

  bool unread(String botId) =>
      (threads[botId] ?? const []).any((t) => t.unread);
}

/// The merged bots of every connected node. A failed load keeps the last
/// good list.
class BotsNotifier extends Notifier<BotsState> {
  var _generation = 0;

  @override
  BotsState build() => const BotsState();

  Future<void> load(GatewayClient? client) async {
    if (client == null) return;
    final gen = ++_generation;
    try {
      final res = BotList.fromJson(await client.call('bot.list', {}));
      if (gen != _generation) return;
      final threads = <String, List<BotThread>>{};
      await Future.wait([
        for (final a in res.bots)
          if (!a.unreachable)
            () async {
              try {
                final r = await client.call('bot.thread.list', {
                  'bot_id': a.id,
                });
                final list = r is Map ? r['threads'] : null;
                threads[a.id] = [
                  if (list is List)
                    for (final t in list)
                      if (t is Map<String, dynamic>) BotThread.fromJson(t),
                ];
              } catch (_) {
                // Keep what was known; the bot screen reports its own errors.
                final known = state.threads[a.id];
                if (known != null) threads[a.id] = known;
              }
            }(),
      ]);
      if (gen != _generation) return;
      state = BotsState(
        bots: res.bots,
        threads: threads,
        loaded: true,
      );
    } catch (e) {
      if (gen != _generation) return;
      state = BotsState(
        bots: state.bots,
        threads: state.threads,
        loaded: state.loaded,
        error: '$e',
      );
    }
  }

  void clear() {
    _generation++;
    state = const BotsState();
  }
}

final botsProvider = NotifierProvider<BotsNotifier, BotsState>(
  BotsNotifier.new,
);
