import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../data/transcript_repository.dart';
import '../models/session.dart';
import '../push/notifications.dart';
import '../state/gateway.dart';
import '../state/sessions.dart';
import '../state/tool_detail.dart';
import '../state/transcript_controller.dart';
import '../transport/connection.dart';
import 'interaction_bar.dart';
import 'live_screen_screen.dart';
import 'respond_sheet.dart';
import 'route_observer.dart';
import 'status_style.dart';
import 'theme.dart';
import 'transcript_feed.dart';

class SessionDetailScreen extends ConsumerStatefulWidget {
  const SessionDetailScreen({super.key, required this.session});

  final Session session;

  @override
  ConsumerState<SessionDetailScreen> createState() =>
      _SessionDetailScreenState();
}

class _SessionDetailScreenState extends ConsumerState<SessionDetailScreen>
    with RouteAware, WidgetsBindingObserver {
  TranscriptSubscription? _sub;

  String get _sid => widget.session.id;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _claimActive();
    WidgetsBinding.instance.addPostFrameCallback((_) => _open());
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    if (route is PageRoute) appRouteObserver.subscribe(this, route);
  }

  // Mark this session as the one on screen and clear any standing notification
  // for it. Called whenever the view becomes visible: on open, when a route
  // pushed over it is popped, and when the app returns to the foreground (which
  // also dismisses a notification the background isolate raised while away).
  void _claimActive() {
    PushNotifications.instance.setActiveSession(_sid);
    unawaited(PushNotifications.instance.cancelForSession(_sid));
  }

  // Stop suppressing this session's notifications, unless something else already
  // became the active session.
  void _releaseActive() {
    if (PushNotifications.instance.activeSessionId == _sid) {
      PushNotifications.instance.setActiveSession(null);
    }
  }

  @override
  void didPopNext() => _claimActive();

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      // Foreground again on this session: re-suppress and dismiss anything that
      // arrived while away.
      if (mounted && (ModalRoute.of(context)?.isCurrent ?? false)) {
        _claimActive();
      }
    } else if (state == AppLifecycleState.paused ||
        state == AppLifecycleState.hidden) {
      // Screen off or backgrounded: you're not actively viewing, so let this
      // session's notifications through.
      _releaseActive();
    }
  }

  void _open() {
    _sub?.dispose();
    _sub = ref.read(transcriptRepositoryProvider).open(
          sessionId: _sid,
          store: ref.read(transcriptProvider(_sid).notifier),
        );
  }

  @override
  void dispose() {
    appRouteObserver.unsubscribe(this);
    WidgetsBinding.instance.removeObserver(this);
    _releaseActive();
    _sub?.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    // Re-open on reconnect (new RpcClient ⇒ stale sub_id).
    ref.listen<ConnState>(connStateProvider, (prev, next) {
      if (next == ConnState.connected && prev != ConnState.connected) {
        _open();
      }
    });

    final st = ref.watch(transcriptProvider(_sid));
    final conn = ref.watch(connStateProvider);
    final s = widget.session;
    final live = ref.watch(sessionsProvider)[_sid] ?? s;
    final title = live.displayTitle;

    return Scaffold(
      appBar: AppBar(
        title: Row(
          children: [
            Text(statusGlyph(live.status),
                style: TextStyle(
                    fontFamily: 'monospace', color: statusColor(live.status))),
            const SizedBox(width: 8),
            Expanded(
                child:
                    Text(title, maxLines: 1, overflow: TextOverflow.ellipsis)),
          ],
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.terminal),
            tooltip: 'Live screen',
            onPressed: () => Navigator.of(context).push(
              MaterialPageRoute(
                builder: (_) => LiveScreenScreen(session: live),
              ),
            ),
          ),
          PopupMenuButton<String>(
            onSelected: (value) async {
              if (value == 'kill') {
                final confirmed = await showDialog<bool>(
                  context: context,
                  builder: (ctx) => AlertDialog(
                    title: const Text('Kill session?'),
                    content: const Text('This cannot be undone.'),
                    actions: [
                      TextButton(
                        onPressed: () => Navigator.of(ctx).pop(false),
                        child: const Text('Cancel'),
                      ),
                      TextButton(
                        onPressed: () => Navigator.of(ctx).pop(true),
                        style: TextButton.styleFrom(
                            foregroundColor: Colors.red),
                        child: const Text('Kill'),
                      ),
                    ],
                  ),
                );
                if (confirmed != true) return;
                final result =
                    await ref.read(sessionRepositoryProvider).kill(live.id);
                if (!context.mounted) return;
                switch (result) {
                  case Ok():
                    Navigator.of(context).pop();
                  case Error(:final error):
                    ScaffoldMessenger.of(context).showSnackBar(
                      SnackBar(content: Text('Failed: $error')),
                    );
                }
              }
            },
            itemBuilder: (_) => [
              const PopupMenuItem<String>(
                value: 'kill',
                child: Text('Kill session'),
              ),
            ],
          ),
        ],
      ),
      // top:false — AppBar already insets the top; we only need the bottom
      // (and side) safe-area so content/InteractionBar clear the system
      // navigation bar (e.g. Android 3-button nav).
      body: SafeArea(
        top: false,
        child: Column(
          children: [
            if (conn != ConnState.connected) _Banner(state: conn),
            Expanded(
              // Spinner until the first snapshot arrives, so an empty session
              // shows progress rather than a blank feed. error stops the spinner.
              child: !st.loaded && st.error == null
                  ? const Center(child: CircularProgressIndicator())
                  : TranscriptFeed(
                      detailRef: ToolDetailRef.live(_sid), chunks: st.chunks),
            ),
            if (live.interaction != null)
              InteractionBar(
                interaction: live.interaction!,
                onRespond: () => showRespondSheet(context, live),
              ),
          ],
        ),
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
