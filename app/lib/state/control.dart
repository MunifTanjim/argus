import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../transport/rpc_client.dart';
import 'gateway.dart';
import 'grouping.dart';

/// Low-level wrapper over the session control RPCs. The UI depends on
/// `SessionRepository` rather than on this service directly; the repository
/// wraps it. Resolves the client fresh on each call so that reconnects (which
/// replace the manager's internal client) are transparent.
///
/// Every method returns a [Result]: a missing client or a failed RPC surfaces
/// as [Error] instead of throwing or silently doing nothing.
class SessionService {
  SessionService(this._clientOf);
  final RpcClient? Function() _clientOf;

  /// Runs [body] against the current client, mapping a null client or any
  /// thrown error to [Error].
  Future<Result<T>> _guard<T>(Future<T> Function(RpcClient c) body) async {
    final c = _clientOf();
    if (c == null) return Result.error(StateError('not connected'));
    try {
      return Result.ok(await body(c));
    } catch (e) {
      return Result.error(e);
    }
  }

  Future<Result<void>> respond(Map<String, dynamic> params) =>
      _guard((c) => c.call('sessions.respond', params));

  Future<Result<void>> sendInput(String sessionId, String text) =>
      _guard((c) => c.call('sessions.input', {
            'session_id': sessionId,
            'text': text,
            'submit': true,
            'prepare': true,
          }));

  Future<Result<String>> capture(String sessionId) => _guard((c) async {
        final r = await c.call('sessions.capture', {'session_id': sessionId});
        return (r as Map?)?['screen'] as String? ?? '';
      });

  Future<Result<void>> sendKeys(String sessionId, List<String> keys) =>
      _guard((c) => c.call('sessions.key', {
            'session_id': sessionId,
            'keys': keys,
          }));

  Future<Result<void>> sendRaw(String sessionId, String text) =>
      _guard((c) => c.call('sessions.input', {
            'session_id': sessionId,
            'text': text,
            'submit': false,
            'prepare': false,
          }));

  Future<Result<void>> spawn({
    String? nodeId,
    String? cwd,
    String? agent,
    required String prompt,
  }) =>
      _guard((c) => c.call('sessions.spawn', {
            'prompt': prompt,
            if (nodeId != null && nodeId.isNotEmpty) 'node_id': nodeId,
            if (cwd != null && cwd.isNotEmpty) 'cwd': cwd,
            if (agent != null && agent.isNotEmpty) 'agent': agent,
          }));

  /// An empty [nodeId] targets the sole node.
  Future<Result<List<AgentInfo>>> listAgents(String? nodeId) =>
      _guard((c) async {
        final r = await c.call('agents.list', {
          if (nodeId != null && nodeId.isNotEmpty) 'node_id': nodeId,
        });
        final list = (r as Map?)?['agents'] as List? ?? const [];
        return [
          for (final a in list)
            AgentInfo(
              id: (a as Map)['id'] as String? ?? '',
              name: a['name'] as String? ?? '',
              color: a['color'] as String? ?? '',
              spawnable: a['spawnable'] as bool? ?? false,
            ),
        ];
      });

  Future<Result<void>> kill(String sessionId) =>
      _guard((c) => c.call('sessions.kill', {'session_id': sessionId}));

  /// Fetches server-wide metadata (version + connected nodes) for the settings
  /// screen (server.info).
  Future<Result<ServerInfo>> serverInfo() => _guard((c) async {
        final r = await c.call('server.info') as Map<String, dynamic>;
        return ServerInfo(
          version: r['version'] as String? ?? '',
          nodes: _parseNodes(r['nodes'] as List? ?? const []),
        );
      });

  /// Lists the nodes connected to the gateway, so the spawn picker can offer a
  /// target even before any session exists on that node. Derived from server.info.
  Future<Result<List<NodeRef>>> nodes() async => switch (await serverInfo()) {
        Ok(:final value) => Result.ok(value.nodes),
        Error(:final error) => Result.error(error),
      };
}

/// Server-wide metadata returned by server.info.
class ServerInfo {
  const ServerInfo({required this.version, required this.nodes});
  final String version;
  final List<NodeRef> nodes;
}

/// Parses node maps (from server.info) into [NodeRef]s, dropping any with an empty
/// id. A gateway always sends non-empty ids; a plain node serves itself with an
/// empty id for its directly-connected TUI, which the app never hits.
List<NodeRef> _parseNodes(List<dynamic> raw) => raw
    .map((e) => e as Map<String, dynamic>)
    .map((m) {
      final id = m['id'] as String? ?? '';
      final label = m['label'] as String?;
      final caps = m['capabilities'] as Map<String, dynamic>?;
      final spawnSupported = caps?['spawn_session'] as bool? ?? false;
      return NodeRef(
        id,
        label != null && label.isNotEmpty ? label : id,
        spawnSupported: spawnSupported,
        version: m['version'] as String? ?? '',
      );
    })
    .where((n) => n.id.isNotEmpty)
    .toList();

final sessionServiceProvider = Provider<SessionService>(
  (ref) => SessionService(() => ref.read(gatewayProvider)?.client),
);

/// One-shot read of server metadata (version + nodes) for the settings screen.
/// Refetches when the connection state changes; resolves to null when the call
/// fails (e.g. not connected).
final serverInfoProvider = FutureProvider.autoDispose<ServerInfo?>((ref) async {
  ref.watch(connStateProvider); // refetch on (re)connect
  final result = await ref.read(sessionServiceProvider).serverInfo();
  return switch (result) {
    Ok(:final value) => value,
    Error() => null,
  };
});
