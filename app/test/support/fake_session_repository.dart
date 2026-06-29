// app/test/support/fake_session_repository.dart
import 'package:argus/core/result.dart';
import 'package:argus/data/session_repository.dart';
import 'package:argus/state/grouping.dart';

/// Base fake for [SessionRepository] where every method succeeds with a no-op
/// result. Tests subclass this and override only the methods they exercise.
class FakeSessionRepository implements SessionRepository {
  @override
  Future<Result<void>> respond(Map<String, dynamic> params) async =>
      const Result.ok(null);

  @override
  Future<Result<void>> sendInput(String sessionId, String text) async =>
      const Result.ok(null);

  @override
  Future<Result<String>> capture(String sessionId) async => const Result.ok('');

  @override
  Future<Result<void>> sendKeys(String sessionId, List<String> keys) async =>
      const Result.ok(null);

  @override
  Future<Result<void>> sendRaw(String sessionId, String text) async =>
      const Result.ok(null);

  @override
  Future<Result<void>> spawn({
    String? nodeId,
    String? cwd,
    required String prompt,
  }) async =>
      const Result.ok(null);

  @override
  Future<Result<void>> kill(String sessionId) async => const Result.ok(null);

  @override
  Future<Result<List<NodeRef>>> nodes() async => const Result.ok(<NodeRef>[]);
}
