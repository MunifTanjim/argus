import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../state/control.dart';
import '../state/grouping.dart';

/// UI-facing entry point for acting on sessions. Screens depend on this
/// abstraction rather than on the transport-level [SessionService], so they can
/// be tested against a lightweight fake and the data source can change without
/// touching the UI. Every method returns a [Result].
abstract class SessionRepository {
  Future<Result<void>> respond(Map<String, dynamic> params);
  Future<Result<void>> sendInput(String sessionId, String text);
  Future<Result<String>> capture(String sessionId);
  Future<Result<void>> sendKeys(String sessionId, List<String> keys);
  Future<Result<void>> sendRaw(String sessionId, String text);
  Future<Result<void>> spawn({
    String? nodeId,
    String? cwd,
    String? agent,
    required String prompt,
  });
  Future<Result<void>> kill(String sessionId);
  Future<Result<List<NodeRef>>> nodes();

  /// Lists every agent [nodeId] knows (empty [nodeId] = sole node).
  Future<Result<List<AgentInfo>>> listAgents(String? nodeId);

  Future<Result<ResumeOutcome>> resume({
    String? nodeId,
    required String agent,
    required String agentSessionId,
    required String cwd,
  });
}

/// [SessionRepository] backed by the gateway over JSON-RPC via [SessionService].
class SessionRepositoryRemote implements SessionRepository {
  SessionRepositoryRemote(this._service);
  final SessionService _service;

  @override
  Future<Result<void>> respond(Map<String, dynamic> params) =>
      _service.respond(params);

  @override
  Future<Result<void>> sendInput(String sessionId, String text) =>
      _service.sendInput(sessionId, text);

  @override
  Future<Result<String>> capture(String sessionId) =>
      _service.capture(sessionId);

  @override
  Future<Result<void>> sendKeys(String sessionId, List<String> keys) =>
      _service.sendKeys(sessionId, keys);

  @override
  Future<Result<void>> sendRaw(String sessionId, String text) =>
      _service.sendRaw(sessionId, text);

  @override
  Future<Result<void>> spawn({
    String? nodeId,
    String? cwd,
    String? agent,
    required String prompt,
  }) =>
      _service.spawn(nodeId: nodeId, cwd: cwd, agent: agent, prompt: prompt);

  @override
  Future<Result<void>> kill(String sessionId) => _service.kill(sessionId);

  @override
  Future<Result<List<NodeRef>>> nodes() => _service.nodes();

  @override
  Future<Result<List<AgentInfo>>> listAgents(String? nodeId) =>
      _service.listAgents(nodeId);

  @override
  Future<Result<ResumeOutcome>> resume({
    String? nodeId,
    required String agent,
    required String agentSessionId,
    required String cwd,
  }) =>
      _service.resume(
        nodeId: nodeId,
        agent: agent,
        agentSessionId: agentSessionId,
        cwd: cwd,
      );
}

final sessionRepositoryProvider = Provider<SessionRepository>(
  (ref) => SessionRepositoryRemote(ref.read(sessionServiceProvider)),
);
