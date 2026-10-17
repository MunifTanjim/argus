import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/enums.dart';
import '../data/transcript_repository.dart';
import '../models/project.dart';
import '../models/session.dart';
import '../push/notifications.dart';
import '../state/control.dart';
import '../state/gateway.dart';
import '../state/navigation.dart';
import '../state/projects.dart';
import '../state/sessions.dart';
import '../state/tasks.dart';
import '../state/tool_detail.dart';
import '../state/transcript_controller.dart';
import '../transport/connection.dart';
import '../transport/jsonrpc.dart';
import 'live_screen_screen.dart';
import 'project_actions.dart';
import 'route_observer.dart';
import 'session_actions.dart';
import 'session_composer.dart';
import 'session_tasks_screen.dart';
import 'status_style.dart';
import 'theme.dart';
import 'transcript_feed.dart';
import 'workspace_screen.dart';

const _routeName = '/session';

// The detail that set the active session. A detail that closes clears only its
// own claim: a scope change builds the new detail before the old one is
// disposed, and both can show the same session.
_SessionDetailScreenState? _claimOwner;

/// Opens [session]'s detail. The home shell finds the live session a scope
/// shows through [sessionDetailRouteId].
Route<void> sessionDetailRoute(Session session) => MaterialPageRoute(
      settings: RouteSettings(name: _routeName, arguments: session.id),
      builder: (_) => SessionDetailScreen(session: session),
    );

String? sessionDetailRouteId(Route<dynamic> route) =>
    route.settings.name == _routeName ? route.settings.arguments as String? : null;

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
  String? _openKey; // the store the stream feeds
  StreamSubscription<RpcMessage>? _tasksSub;
  RouteObserver<PageRoute<dynamic>>? _observer;

  String get _sid => widget.session.id;

  // Agent session id changes on /clear; pre-clear entries must not leak into the
  // post-clear store. Falls back to argus id before a hook sets one.
  String _keyFor(String? cid) =>
      (cid != null && cid.isNotEmpty) ? cid : _sid;
  String _cacheKey(Session? s) => _keyFor(s?.agentSessionId);

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addObserver(this);
    _claimActive();
    _bindTasks();
    WidgetsBinding.instance.addPostFrameCallback((_) => _open());
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    final route = ModalRoute.of(context);
    final observer = ShellNavigatorScope.observerOf(context);
    if (observer != _observer) {
      _observer?.unsubscribe(this);
      _observer = observer;
    }
    if (route is PageRoute) observer.subscribe(this, route);
  }

  bool get _shown => mounted && (ModalRoute.of(context)?.isCurrent ?? false);

  // Mark this session as the one on screen and clear any standing notification
  // for it. Called whenever the view becomes visible: on open, when a route
  // pushed over it is popped, and when the app returns to the foreground (which
  // also dismisses a notification the background isolate raised while away).
  void _claimActive() {
    _claimOwner = this;
    PushNotifications.instance.setActiveSession(_sid);
    unawaited(PushNotifications.instance.cancelForSession(_sid));
  }

  // Stop suppressing this session's notifications, unless something else already
  // became the active session.
  void _releaseActive() {
    if (_claimOwner != this) return;
    _claimOwner = null;
    if (PushNotifications.instance.activeSessionId == _sid) {
      PushNotifications.instance.setActiveSession(null);
    }
  }

  @override
  void didPopNext() {
    if (_shown) _claimActive();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    if (state == AppLifecycleState.resumed) {
      // Foreground again on this session: re-suppress and dismiss anything that
      // arrived while away.
      if (_shown) _claimActive();
    } else if (state == AppLifecycleState.paused ||
        state == AppLifecycleState.hidden) {
      // Screen off or backgrounded: you're not actively viewing, so let this
      // session's notifications through.
      _releaseActive();
    }
  }

  void _open() {
    _sub?.dispose();
    final live = ref.read(sessionsProvider)[_sid] ?? widget.session;
    _openKey = _cacheKey(live);
    _sub = ref.read(transcriptRepositoryProvider).open(
          sessionId: _sid,
          store: ref.read(transcriptProvider(_openKey!).notifier),
        );
  }

  // Refetches the task list on each tasks.changed push for this session; the
  // tasks screen pushed over this one reads the same provider. Each reconnect
  // builds a new client, so this rebinds on connect.
  void _bindTasks() {
    _tasksSub?.cancel();
    _tasksSub = ref.read(gatewayProvider)?.client?.notifications.listen((m) {
      final params = m.params;
      if (m.method == 'tasks.changed' &&
          params is Map &&
          params['session_id'] == _sid) {
        ref.invalidate(tasksProvider(_sid));
      }
    });
  }

  @override
  void dispose() {
    _observer?.unsubscribe(this);
    WidgetsBinding.instance.removeObserver(this);
    _releaseActive();
    _sub?.dispose();
    _tasksSub?.cancel();
    super.dispose();
  }

  // The pops forget the details they close; clearing the remembered session
  // keeps the workspace from reopening a detail over it.
  void _showWorkspace(String workspaceId) {
    Navigator.of(context).popUntil((r) => r.isFirst);
    ref.read(rememberedSessionsProvider.notifier).clear(workspaceId);
    ref.read(scopeProvider.notifier).state = workspaceId;
  }

  @override
  Widget build(BuildContext context) {
    // Re-open on reconnect (new RpcClient ⇒ stale sub_id).
    ref.listen<ConnState>(connStateProvider, (prev, next) {
      if (next == ConnState.connected && prev != ConnState.connected) {
        _open();
        _bindTasks();
      }
    });
    // Re-open when the cache key changes (/clear or first hook set), or when
    // the session comes back online: a restarted node drops its subscriptions
    // while this connection stays up, and after a gateway restart the session
    // is re-listed only once its node returns. A missing session keeps the
    // stream's key.
    ref.listen<Session?>(
      sessionsProvider.select((m) => m[_sid]),
      (prev, next) {
        if (next == null) return;
        final back = !next.offline && (prev == null || prev.offline);
        final prevKey = _openKey;
        if (_cacheKey(next) == prevKey && !back) return;
        _open();
        if (prevKey != null && prevKey != _openKey) {
          ref.invalidate(transcriptProvider(prevKey));
        }
      },
    );

    final s = widget.session;
    final live = ref.watch(sessionsProvider)[_sid] ?? s;
    // A node older than the app's minimum may not understand our requests.
    final outdated = ref.watch(
        outdatedNodeIdsProvider.select((ids) => ids.contains(live.nodeId)));
    final canKill = live.canKill && !outdated;
    final canOpenTerminal = live.canOpenTerminal && !outdated;
    final ix = live.interaction;
    final pendingPrompt = ix != null && ix.kind != InteractionKind.idle;
    final String? inputNotice;
    if (outdated) {
      inputNotice = 'Upgrade argus on ${live.nodeLabel ?? 'this node'}';
    } else if (!live.acceptsInput && !pendingPrompt) {
      inputNotice = respondElsewhereLabel(live.frontend);
    } else {
      inputNotice = null;
    }
    final st = ref.watch(transcriptProvider(_openKey ?? _cacheKey(live)));
    final conn = ref.watch(connStateProvider);
    final connError = ref.watch(connErrorProvider);
    final title = live.displayTitle;
    final tasks = ref.watch(tasksProvider(_sid));
    final hasTasks = !tasks.hasError && (tasks.value?.isNotEmpty ?? false);
    final wsId = live.workspaceId;
    final hasWorkspace = wsId != null &&
        wsId.isNotEmpty &&
        ref.watch(projectsProvider
            .select((p) => lookupWorkspace(p.projects, wsId) != null));

    return Scaffold(
      appBar: AppBar(
        titleSpacing: 0,
        title: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Row(
              children: [
                Text(statusGlyph(live.status),
                    style: TextStyle(
                        fontFamily: 'monospace',
                        color: statusColor(live.status))),
                const SizedBox(width: 8),
                Flexible(
                    child: Text(title,
                        maxLines: 1, overflow: TextOverflow.ellipsis)),
                if (live.branch?.isNotEmpty ?? false)
                  Tooltip(
                    message: live.branch!,
                    triggerMode: TooltipTriggerMode.tap,
                    child: const Padding(
                      padding: EdgeInsets.only(left: 8),
                      child: Icon(Icons.commit, size: 18),
                    ),
                  ),
                const Spacer(),
                if (live.frontend != FrontendKind.tmux)
                  Padding(
                    padding: const EdgeInsets.only(left: 8),
                    child: Chip(
                      label: Text(live.frontend.name),
                      padding: EdgeInsets.zero,
                      labelPadding:
                          const EdgeInsets.symmetric(horizontal: 6),
                      materialTapTargetSize: MaterialTapTargetSize.shrinkWrap,
                      visualDensity: VisualDensity.compact,
                    ),
                  ),
              ],
            ),
            if (live.displayName != null)
              Text(live.displayName!,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: mono.copyWith(color: AppColors.dim)),
          ],
        ),
        actions: [
          IconButton(
            icon: const Icon(Icons.terminal),
            tooltip: 'Live Screen',
            onPressed: canOpenTerminal
                ? () => Navigator.of(context).push(
                      MaterialPageRoute(
                        builder: (_) => LiveScreenScreen(session: live),
                      ),
                    )
                : null,
          ),
          PopupMenuButton<String>(
            position: PopupMenuPosition.under,
            onSelected: (value) async {
              if (value == 'tasks') {
                Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => SessionTasksScreen(session: live),
                  ),
                );
              } else if (value == 'workspace' && hasWorkspace) {
                _showWorkspace(wsId);
              } else if (value == 'changes' && hasWorkspace) {
                Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => WorkspaceChangesScreen(workspaceId: wsId),
                  ),
                );
              } else if (value == 'files' && hasWorkspace) {
                Navigator.of(context).push(
                  MaterialPageRoute(
                    builder: (_) => WorkspaceFilesScreen(workspaceId: wsId),
                  ),
                );
              } else if (value == 'rename') {
                if (outdated) return;
                await renameSession(ActionContext.of(context), live);
              } else if (value == 'kill') {
                if (!canKill) return;
                final killed = await killSession(ActionContext.of(context), live);
                if (killed && context.mounted) Navigator.of(context).pop();
              }
            },
            itemBuilder: (_) => [
              if (hasTasks)
                const PopupMenuItem<String>(
                  value: 'tasks',
                  child: ListTile(
                    leading: Icon(Icons.checklist),
                    title: Text('Tasks'),
                    contentPadding: EdgeInsets.zero,
                  ),
                ),
              if (hasWorkspace) ...const [
                PopupMenuItem<String>(
                  value: 'workspace',
                  child: ListTile(
                    leading: Icon(Icons.workspaces_outlined),
                    title: Text('Workspace'),
                    contentPadding: EdgeInsets.zero,
                  ),
                ),
                PopupMenuItem<String>(
                  value: 'changes',
                  child: ListTile(
                    leading: Icon(Icons.difference),
                    title: Text('Changes'),
                    contentPadding: EdgeInsets.zero,
                  ),
                ),
                PopupMenuItem<String>(
                  value: 'files',
                  child: ListTile(
                    leading: Icon(Icons.folder_outlined),
                    title: Text('Files'),
                    contentPadding: EdgeInsets.zero,
                  ),
                ),
              ],
              PopupMenuItem<String>(
                value: 'rename',
                enabled: !outdated,
                child: const ListTile(
                  leading: Icon(Icons.edit_outlined),
                  title: Text('Rename Session'),
                  contentPadding: EdgeInsets.zero,
                ),
              ),
              PopupMenuItem<String>(
                value: 'kill',
                enabled: canKill,
                child: const ListTile(
                  leading: Icon(Icons.dangerous),
                  title: Text('Kill Session'),
                  contentPadding: EdgeInsets.zero,
                ),
              ),
            ],
          ),
        ],
      ),
      // top:false — AppBar already insets the top; we only need the bottom
      // (and side) safe-area so content/SessionComposer clear the system
      // navigation bar (e.g. Android 3-button nav).
      body: SafeArea(
        top: false,
        child: Column(
          children: [
            if (conn != ConnState.connected)
              _Banner(state: conn, message: connError),
            Expanded(
              // The composer is capped to the body, which the keyboard
              // shrinks; a dragged prompt can fill all of it.
              child: LayoutBuilder(
                builder: (context, box) => Column(
                  children: [
                    Expanded(
                      // Spinner until the first snapshot arrives; error stops it.
                      child: switch (live.status) {
                        SessionStatus.starting => const _StartingNotice(),
                        _ when !st.loaded && st.error == null =>
                          const Center(child: CircularProgressIndicator()),
                        _ => TranscriptFeed(
                            detailRef:
                                ToolDetailRef.live(_sid, agent: live.agent),
                            entries: st.entries),
                      },
                    ),
                    if (live.status != SessionStatus.dead)
                      ConstrainedBox(
                        constraints: BoxConstraints(maxHeight: box.maxHeight),
                        child: SessionComposer(
                            session: live,
                            inputNotice: inputNotice,
                            queue: st.queue),
                      ),
                  ],
                ),
              ),
            ),
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

class _StartingNotice extends StatelessWidget {
  const _StartingNotice();

  @override
  Widget build(BuildContext context) {
    return const Center(
      child: Padding(
        padding: EdgeInsets.symmetric(horizontal: 32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.hourglass_empty,
                size: 48, color: AppColors.secondary),
            SizedBox(height: 16),
            Text(
              'Session is starting or waiting at a startup prompt.',
              textAlign: TextAlign.center,
              style: TextStyle(fontWeight: FontWeight.w600),
            ),
            SizedBox(height: 8),
            Text(
              'Tap the terminal icon to open the live screen and continue.',
              textAlign: TextAlign.center,
              style: TextStyle(color: AppColors.secondary),
            ),
          ],
        ),
      ),
    );
  }
}
