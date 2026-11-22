import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/pagent.dart';
import '../transport/gateway_client.dart';
import 'gateway.dart';

/// Wraps the persistent-agent RPCs. Ids are composite; the E2E client routes
/// them to their node.
class PAgentsApi {
  PAgentsApi(this._clientOf);
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

  Future<PAgentList> list({bool includeArchived = false}) async => PAgentList
      .fromJson(
        await _call('pagent.list', {
          if (includeArchived) 'include_archived': true,
        }),
      );

  Future<PAgent> create({
    String? name,
    String? dir,
    String? agent,
    String? model,
    String? from,
    String? nodeId,
  }) async => PAgent.fromJson(
    await _map('pagent.create', {
      if (name != null) 'name': name,
      if (dir != null) 'dir': dir,
      if (agent != null) 'agent': agent,
      if (model != null) 'model': model,
      if (from != null) 'from': from,
      if (nodeId != null) 'node_id': nodeId,
    }),
  );

  Future<PAgentAddResult> add(
    String dir, {
    String? name,
    bool importInstructions = false,
    String? nodeId,
  }) async => PAgentAddResult.fromJson(
    await _map('pagent.add', {
      'dir': dir,
      if (name != null) 'name': name,
      if (importInstructions) 'import_instructions': true,
      if (nodeId != null) 'node_id': nodeId,
    }),
  );

  Future<PAgent> show(String agentId) async =>
      PAgent.fromJson(await _map('pagent.show', {'agent_id': agentId}));

  Future<PAgent> rename(String agentId, String name) async => PAgent.fromJson(
    await _map('pagent.rename', {'agent_id': agentId, 'name': name}),
  );

  Future<void> forget(String agentId, {bool purge = false}) => _call(
    'pagent.forget',
    {'agent_id': agentId, if (purge) 'purge': true},
  );

  /// A null [agentId] unsets the primary on every node.
  Future<void> setPrimary(String? agentId) =>
      _call('pagent.setPrimary', {if (agentId != null) 'agent_id': agentId});

  Future<PAgentSendResult> send(
    String agentId,
    String text, {
    String? threadId,
    bool newThread = false,
  }) async => PAgentSendResult.fromJson(
    await _map('pagent.thread.send', {
      'agent_id': agentId,
      if (threadId != null) 'thread_id': threadId,
      'text': text,
      if (newThread) 'new': true,
    }),
  );

  Future<List<PAgentThread>> threads(String agentId) async {
    final res = await _map('pagent.thread.list', {'agent_id': agentId});
    return [
      for (final t in (res['threads'] as List?) ?? const [])
        if (t is Map<String, dynamic>) PAgentThread.fromJson(t),
    ];
  }

  /// [position] is a thread's lastActiveAt in Unix nanoseconds; null means now.
  Future<void> markRead(String threadId, {int? position}) => _call(
    'pagent.thread.markRead',
    {'thread_id': threadId, if (position != null) 'position': position},
  );

  Future<PAgentTimeline> timeline(String threadId) async =>
      PAgentTimeline.fromJson(
        await _map('pagent.thread.timeline', {'thread_id': threadId}),
      );

  Future<PAgentMemory> memory(String agentId) async =>
      PAgentMemory.fromJson(
        await _map('pagent.memory.show', {'agent_id': agentId}),
      );

  /// [profile] lists the shared profile's commits instead of the agent's.
  Future<List<PAgentMemoryCommit>> memoryLog(
    String agentId, {
    int? limit,
    bool profile = false,
  }) async {
    final res = await _map('pagent.memory.log', {
      'agent_id': agentId,
      if (limit != null) 'limit': limit,
      if (profile) 'profile': true,
    });
    return [
      for (final c in (res['commits'] as List?) ?? const [])
        if (c is Map<String, dynamic>) PAgentMemoryCommit.fromJson(c),
    ];
  }

  /// Reverts [commit] and returns the revert commit's hash.
  Future<String> memoryUndo(
    String agentId,
    String commit, {
    bool profile = false,
  }) async {
    final res = await _map('pagent.memory.undo', {
      'agent_id': agentId,
      'commit': commit,
      if (profile) 'profile': true,
    });
    return res['commit'] as String? ?? '';
  }

  Future<List<PAgentMemoryProposal>> proposals(String agentId) async {
    final res = await _map('pagent.memory.proposals', {'agent_id': agentId});
    return [
      for (final p in (res['proposals'] as List?) ?? const [])
        if (p is Map<String, dynamic>) PAgentMemoryProposal.fromJson(p),
    ];
  }

  Future<PAgentDecideResult> decide(String proposalId, bool accept) async =>
      PAgentDecideResult.fromJson(
        await _map('pagent.memory.decide', {
          'proposal_id': proposalId,
          'accept': accept,
        }),
      );

  /// Returns how many changes were staged for review.
  Future<int> importMemory(String agentId, String content) async {
    final res = await _map('pagent.memory.import', {
      'agent_id': agentId,
      'content': content,
    });
    return (res['staged'] as num?)?.toInt() ?? 0;
  }
}

/// The connected client, or null. Tests override it.
final pagentsClientProvider = Provider<GatewayClient?>(
  (ref) => ref.read(gatewayProvider)?.client,
);

final pagentsApiProvider = Provider<PAgentsApi>(
  (ref) => PAgentsApi(() => ref.read(pagentsClientProvider)),
);

/// The size of an agent's core memory, its budget, and when it last changed
/// (null when unknown).
class AgentMemoryUsage {
  const AgentMemoryUsage({this.usage = 0, this.budget = 0, this.last});
  final int usage;
  final int budget;
  final DateTime? last;
}

/// The agent's memory usage, or null when the node has no memory to show.
/// Reloads when the agent list changes.
final agentMemoryUsageProvider = FutureProvider.autoDispose
    .family<AgentMemoryUsage?, String>((ref, agentId) async {
      ref.watch(pagentsProvider.select((s) => s.agents));
      final api = ref.read(pagentsApiProvider);
      final PAgentMemory mem;
      try {
        mem = await api.memory(agentId);
      } catch (_) {
        return null;
      }
      DateTime? last;
      try {
        final log = await api.memoryLog(agentId, limit: 1);
        if (log.isNotEmpty) last = log.first.when;
      } catch (_) {}
      return AgentMemoryUsage(usage: mem.usage, budget: mem.budget, last: last);
    });

class PAgentsState {
  const PAgentsState({
    this.agents = const [],
    this.primary = '',
    this.threads = const {},
    this.loaded = false,
    this.error,
  });

  final List<PAgent> agents;
  // Each reachable agent's threads; an agent not listed has none loaded yet.
  final Map<String, List<PAgentThread>> threads;
  final String primary; // the primary agent's composite id, or empty
  final bool loaded;
  final String? error; // the last load failed; agents are the last good list

  bool isPrimary(PAgent a) => a.primary || (a.id.isNotEmpty && a.id == primary);

  bool unread(String agentId) =>
      (threads[agentId] ?? const []).any((t) => t.unread);

  PAgent? get primaryAgent {
    for (final a in agents) {
      if (a.id == primary) return a;
    }
    return null;
  }
}

/// The merged agents of every connected node. A failed load keeps the last
/// good list.
class PAgentsNotifier extends Notifier<PAgentsState> {
  var _generation = 0;

  @override
  PAgentsState build() => const PAgentsState();

  Future<void> load(GatewayClient? client) async {
    if (client == null) return;
    final gen = ++_generation;
    try {
      final res = PAgentList.fromJson(await client.call('pagent.list', {}));
      if (gen != _generation) return;
      final threads = <String, List<PAgentThread>>{};
      await Future.wait([
        for (final a in res.agents)
          if (!a.unreachable)
            () async {
              try {
                final r = await client.call('pagent.thread.list', {
                  'agent_id': a.id,
                });
                final list = r is Map ? r['threads'] : null;
                threads[a.id] = [
                  if (list is List)
                    for (final t in list)
                      if (t is Map<String, dynamic>) PAgentThread.fromJson(t),
                ];
              } catch (_) {
                // Keep what was known; the agent screen reports its own errors.
                final known = state.threads[a.id];
                if (known != null) threads[a.id] = known;
              }
            }(),
      ]);
      if (gen != _generation) return;
      state = PAgentsState(
        agents: res.agents,
        primary: res.primary,
        threads: threads,
        loaded: true,
      );
    } catch (e) {
      if (gen != _generation) return;
      state = PAgentsState(
        agents: state.agents,
        primary: state.primary,
        threads: state.threads,
        loaded: state.loaded,
        error: '$e',
      );
    }
  }

  void clear() {
    _generation++;
    state = const PAgentsState();
  }
}

final pagentsProvider = NotifierProvider<PAgentsNotifier, PAgentsState>(
  PAgentsNotifier.new,
);
