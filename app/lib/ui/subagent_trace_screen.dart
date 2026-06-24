import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/transcript_repository.dart';
import '../models/chunk.dart';
import '../state/gateway.dart';
import '../state/tool_detail.dart';
import '../state/transcript_controller.dart';
import '../transport/connection.dart';
import 'theme.dart';
import 'transcript_feed.dart';

class SubagentTraceScreen extends ConsumerStatefulWidget {
  const SubagentTraceScreen(
      {super.key, required this.parentRef, required this.item});

  /// The transcript this subagent was spawned from; the trace's tool bodies are
  /// fetched against it scoped to the subagent ([ToolDetailRef.forAgent]).
  final ToolDetailRef parentRef;
  final Item item;

  @override
  ConsumerState<SubagentTraceScreen> createState() =>
      _SubagentTraceScreenState();
}

class _SubagentTraceScreenState extends ConsumerState<SubagentTraceScreen> {
  TranscriptSubscription? _sub;

  bool get _inline => widget.item.trace.isNotEmpty;
  String? get _agentId => widget.item.agentId;
  String? get _sessionId => widget.parentRef.sessionId;
  String get _key => '${_sessionId ?? ''}/${_agentId ?? ''}';
  ToolDetailRef get _traceRef => widget.parentRef.forAgent(_agentId);

  @override
  void initState() {
    super.initState();
    if (!_inline &&
        (_agentId?.isNotEmpty ?? false) &&
        (_sessionId?.isNotEmpty ?? false)) {
      WidgetsBinding.instance.addPostFrameCallback((_) => _open());
    }
  }

  void _open() {
    _sub?.dispose();
    _sub = ref.read(transcriptRepositoryProvider).open(
          sessionId: _sessionId!,
          agentId: _agentId,
          store: ref.read(transcriptProvider(_key).notifier),
        );
  }

  @override
  void dispose() {
    _sub?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final title = widget.item.subagentType ?? 'Subagent';

    if (_inline) {
      return Scaffold(
        appBar: AppBar(title: Text(title)),
        body: TranscriptFeed(
            detailRef: _traceRef,
            chunks: widget.item.trace,
            stickToBottom: false), // inlined trace is complete; read top-down
      );
    }

    // Re-open on reconnect (new RpcClient ⇒ stale sub_id).
    ref.listen<ConnState>(connStateProvider, (prev, next) {
      if (next == ConnState.connected && prev != ConnState.connected) {
        _open();
      }
    });

    final st = ref.watch(transcriptProvider(_key));
    final conn = ref.watch(connStateProvider);

    return Scaffold(
      appBar: AppBar(title: Text(title)),
      body: Column(
        children: [
          if (conn != ConnState.connected) _Banner(state: conn),
          Expanded(
              child: TranscriptFeed(detailRef: _traceRef, chunks: st.chunks)),
        ],
      ),
    );
  }
}

class _Banner extends StatelessWidget {
  const _Banner({required this.state});
  final ConnState state;

  @override
  Widget build(BuildContext context) {
    final text = switch (state) {
      ConnState.connecting => 'Connecting…',
      ConnState.reconnecting => 'Reconnecting…',
      ConnState.disconnected => 'Disconnected',
      ConnState.connected => 'Connected',
    };
    return Container(
      width: double.infinity,
      color: AppColors.awaitingSurface,
      padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 12),
      child: Text(text,
          style: const TextStyle(color: AppColors.secondary, fontSize: 12)),
    );
  }
}
