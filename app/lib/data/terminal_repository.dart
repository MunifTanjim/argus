import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/enums.dart';
import '../state/gateway.dart';
import '../state/terminal_controller.dart';
import '../transport/rpc_client.dart';

/// UI-facing entry point for a live terminal attach, so screens don't construct
/// a [TerminalAttach] against the raw client directly.
abstract class TerminalRepository {
  /// Opens a live attach. Returns null when there is no connection yet (e.g.
  /// before the first connect); the caller re-opens on reconnect.
  TerminalSession? open({
    required String sessionId,
    required int cols,
    required int rows,
    required void Function(List<int> data) onData,
    void Function(TerminalExitReason reason)? onExited,
    void Function(Object error)? onError,
  });
}

/// [TerminalRepository] backed by the gateway connection. Resolves the client
/// fresh on each open so reconnects use the new client.
class TerminalRepositoryRemote implements TerminalRepository {
  TerminalRepositoryRemote(this._clientOf);
  final RpcClient? Function() _clientOf;

  @override
  TerminalSession? open({
    required String sessionId,
    required int cols,
    required int rows,
    required void Function(List<int> data) onData,
    void Function(TerminalExitReason reason)? onExited,
    void Function(Object error)? onError,
  }) {
    final client = _clientOf();
    if (client == null) return null;
    return TerminalAttach(
      client: client,
      sessionId: sessionId,
      cols: cols,
      rows: rows,
      onData: onData,
      onExited: onExited,
      onError: onError,
    )..start();
  }
}

final terminalRepositoryProvider = Provider<TerminalRepository>(
  (ref) => TerminalRepositoryRemote(() => ref.read(gatewayProvider)?.client),
);
