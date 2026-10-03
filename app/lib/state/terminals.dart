import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../e2e/aggregate.dart' show sortByNode;
import '../models/terminal.dart';
import '../transport/gateway_client.dart';

class TerminalsState {
  const TerminalsState({
    this.terminals = const [],
    this.loaded = false,
    this.error,
  });

  final List<NodeTerminal> terminals;
  final bool loaded;
  final String? error; // the last load failed; terminals are the last good list
}

/// Every connected node's terminals. A node that failed while others answered
/// keeps its last good terminals.
class TerminalsNotifier extends Notifier<TerminalsState> {
  var _generation = 0;

  @override
  TerminalsState build() => const TerminalsState();

  Future<void> load(GatewayClient? client) async {
    if (client == null) return;
    final gen = ++_generation;
    try {
      final res = await client.call('terminal.list');
      if (gen != _generation) return;
      final failed = res is Map ? res['failed_nodes'] : null;
      final kept = failed is List && failed.isNotEmpty
          ? [
              for (final t in state.terminals)
                if (failed.contains(t.nodeId)) t,
            ]
          : const <NodeTerminal>[];
      state = TerminalsState(
        terminals: sortByNode(
          [...parseTerminalList(res), ...kept],
          (t) => '${t.nodeLabel}\u0000${t.nodeId}',
        ),
        loaded: true,
      );
    } catch (e) {
      if (gen != _generation) return;
      state = TerminalsState(
        terminals: state.terminals,
        loaded: state.loaded,
        error: '$e',
      );
    }
  }

  void clear() {
    _generation++;
    state = const TerminalsState();
  }
}

final terminalsProvider = NotifierProvider<TerminalsNotifier, TerminalsState>(
  TerminalsNotifier.new,
);
