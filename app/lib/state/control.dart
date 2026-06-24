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
    required String name,
    String? cwd,
    String? command,
  }) =>
      _guard((c) => c.call('sessions.spawn', {
            'name': name,
            if (nodeId != null && nodeId.isNotEmpty) 'node_id': nodeId,
            if (cwd != null && cwd.isNotEmpty) 'cwd': cwd,
            if (command != null && command.isNotEmpty) 'command': command,
          }));

  Future<Result<void>> kill(String sessionId) =>
      _guard((c) => c.call('sessions.kill', {'session_id': sessionId}));

  /// Lists the nodes connected to the gateway, so the spawn picker can offer a
  /// target even before any session exists on that node.
  Future<Result<List<NodeRef>>> nodes() => _guard((c) async {
        final result = await c.call('nodes.list');
        return (result as List)
            .map((e) => e as Map<String, dynamic>)
            .map((m) {
              final id = m['node_id'] as String? ?? '';
              final label = m['node_label'] as String?;
              return NodeRef(id, label != null && label.isNotEmpty ? label : id);
            })
            .where((n) => n.id.isNotEmpty)
            .toList();
      });
}

final sessionServiceProvider = Provider<SessionService>(
  (ref) => SessionService(() => ref.read(gatewayProvider)?.client),
);
