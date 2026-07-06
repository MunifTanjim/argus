import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/history_repository.dart';
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
  Future<Result<List<Chunk>>>? _histFuture;

  bool get _inline => widget.item.soleSubagent?.trace.isNotEmpty ?? false;
  String? get _agentId => widget.item.soleSubagent?.id;
  String? get _sessionId => widget.parentRef.sessionId;
  bool get _isHistory => widget.parentRef.isHistory;
  String get _key => '${_sessionId ?? ''}/${_agentId ?? ''}';
  ToolDetailRef get _traceRef => widget.parentRef.forAgent(_agentId);

  @override
  void initState() {
    super.initState();
    if (_inline || (_agentId?.isEmpty ?? true)) return;
    if (_isHistory) {
      _histFuture = ref.read(historyRepositoryProvider).transcript(
            nodeId: widget.parentRef.nodeId,
            transcriptPath: widget.parentRef.transcriptPath!,
            agentId: _agentId,
          );
    } else if (_sessionId?.isNotEmpty ?? false) {
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
    final sub = widget.item.soleSubagent;
    final title = (sub?.name.isNotEmpty ?? false)
        ? sub!.name
        : (sub?.type ?? 'Subagent');

    if (_inline) {
      return Scaffold(
        appBar: AppBar(title: Text(title)),
        body: SafeArea(
          top: false, // AppBar insets top; bottom clears the system nav bar.
          child: TranscriptFeed(
              detailRef: _traceRef,
              chunks: widget.item.soleSubagent?.trace ?? const [],
              stickToBottom: false), // inlined trace is complete; read top-down
        ),
      );
    }

    if (_isHistory) {
      return Scaffold(
        appBar: AppBar(title: Text(title)),
        body: SafeArea(
          top: false, // AppBar insets top; bottom clears the system nav bar.
          child: FutureBuilder<Result<List<Chunk>>>(
            future: _histFuture,
            builder: (context, snap) {
              final data = snap.data;
              if (data == null) {
                return const Center(child: CircularProgressIndicator());
              }
              return switch (data) {
                Ok(value: final chunks) => TranscriptFeed(
                    detailRef: _traceRef,
                    chunks: chunks,
                    stickToBottom: false),
                Error(error: final e) =>
                  Center(child: Text('Failed to load trace: $e')),
              };
            },
          ),
        ),
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
    final connError = ref.watch(connErrorProvider);

    return Scaffold(
      appBar: AppBar(title: Text(title)),
      body: SafeArea(
        top: false, // AppBar insets top; bottom clears the system nav bar.
        child: Column(
          children: [
            if (conn != ConnState.connected)
              _Banner(state: conn, message: connError),
            Expanded(
                child: TranscriptFeed(detailRef: _traceRef, chunks: st.chunks)),
          ],
        ),
      ),
    );
  }
}

class _Banner extends StatelessWidget {
  const _Banner({required this.state, this.message});
  final ConnState state;
  final String? message;

  @override
  Widget build(BuildContext context) {
    final failed = state == ConnState.failed;
    final text = switch (state) {
      ConnState.connecting => 'Connecting…',
      ConnState.reconnecting => 'Reconnecting…',
      ConnState.disconnected => 'Disconnected',
      ConnState.connected => 'Connected',
      ConnState.failed => message ?? 'Connection failed',
    };
    return Container(
      width: double.infinity,
      color: failed ? AppColors.errorSurface : AppColors.awaitingSurface,
      padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 12),
      child: Text(text,
          style: TextStyle(
              color: failed ? AppColors.error : AppColors.secondary,
              fontSize: 12)),
    );
  }
}
