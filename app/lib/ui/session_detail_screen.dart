import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../data/transcript_repository.dart';
import '../models/enums.dart';
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
import 'details_header.dart';
import 'live_screen_screen.dart';
import 'project_actions.dart';
import 'route_observer.dart';
import 'session_actions.dart';
import 'session_composer.dart';
import 'session_chips.dart';
import 'session_view_header.dart';
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
  // The session left the registry after this screen saw it.
  bool _gone = false;
  StreamSubscription<RpcMessage>? _tasksSub;
  RouteObserver<PageRoute<dynamic>>? _observer;
  final _feedKey = GlobalKey<TranscriptFeedState>();
  // The large header is on screen; the app bar title stays hidden. A notifier,
  // so a change rebuilds only the title and not the transcript.
  final _headerVisible = ValueNotifier(true);
  // The chip row's height; the transcript makes this much room under it.
  double _chipsHeight = 0;

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
    _headerVisible.dispose();
    super.dispose();
  }

  // The pops forget the details they close; clearing the remembered session
  // keeps the workspace from reopening a detail over it.
  void _showWorkspace(String workspaceId) {
    Navigator.of(context).popUntil((r) => r.isFirst);
    ref.read(rememberedSessionsProvider.notifier).clear(workspaceId);
    ref.read(scopeProvider.notifier).state = workspaceId;
  }

  // The large header's details, for when it is far above in the transcript.
  // The sheet follows the live session; the transcript keeps its place.
  void _showDetails() {
    showDetailsSheet(
      context,
      (_) => Consumer(
        builder: (context, ref, _) => SessionViewHeader(
          session:
              ref.watch(sessionsProvider.select((m) => m[_sid])) ??
              widget.session,
          wrap: true,
        ),
      ),
    );
  }

  // The large header over a state with no feed; it stays in view, so the app
  // bar title stays hidden.
  Widget _withHeader(Session live, Widget body) {
    if (!_headerVisible.value) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _headerVisible.value = true;
      });
    }
    return Column(children: [
      Padding(
        padding: const EdgeInsets.fromLTRB(12, 12, 12, 0),
        child: SessionViewHeader(session: live),
      ),
      Expanded(child: body),
    ]);
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
        if (next == null) {
          if (prev != null) setState(() => _gone = true);
          return;
        }
        if (_gone) setState(() => _gone = false);
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
    // Rebuild for this session's updates only, not every session's.
    final live = ref.watch(sessionsProvider.select((m) => m[_sid])) ?? s;
    // A node older than the app's minimum may not understand our requests.
    final outdated = ref.watch(
        outdatedNodeIdsProvider.select((ids) => ids.contains(live.nodeId)));
    final canKill = live.canKill && !outdated;
    final canOpenTerminal = live.canOpenTerminal && !outdated;
    final pendingPrompt = live.interactionPending;
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
    final wsId = live.workspaceId;
    final hasWorkspace = wsId != null &&
        wsId.isNotEmpty &&
        ref.watch(projectsProvider
            .select((p) => lookupWorkspace(p.projects, wsId) != null));

    return Scaffold(
      appBar: AppBar(
        titleSpacing: 0,
        // The large header at the top of the transcript holds the details;
        // this one-line title shows once that header scrolls away.
        // Hidden, it takes no taps; shown, a tap opens the details in place.
        title: ValueListenableBuilder(
          valueListenable: _headerVisible,
          builder: (_, headerVisible, title) => IgnorePointer(
            ignoring: headerVisible,
            child: AnimatedOpacity(
              opacity: headerVisible ? 0 : 1,
              duration: const Duration(milliseconds: 150),
              child: title,
            ),
          ),
          child: Align(
            alignment: Alignment.centerLeft,
            child: InkWell(
              borderRadius: BorderRadius.circular(context.tokens.radius),
              onTap: _showDetails,
              child: Padding(
                padding: const EdgeInsets.symmetric(vertical: 4),
                child: SessionTitle(session: live),
              ),
            ),
          ),
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
              if (value == 'workspace' && hasWorkspace) {
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
                      // The chips float over the transcript's bottom edge, so
                      // the transcript shows around them.
                      child: Stack(
                        children: [
                          Positioned.fill(
                            // Spinner until the first snapshot arrives; error
                            // stops it.
                            child: switch (live.status) {
                              SessionStatus.starting => _withHeader(
                                live,
                                const _StartingNotice(),
                              ),
                              _ when !st.loaded && st.error == null =>
                                _withHeader(
                                  live,
                                  const Center(
                                    child: CircularProgressIndicator(),
                                  ),
                                ),
                              _ => TranscriptFeed(
                                key: _feedKey,
                                detailRef: ToolDetailRef.live(
                                  _sid,
                                  agent: live.agent,
                                ),
                                entries: st.entries,
                                bottomInset: _chipsHeight,
                                live: live.status == SessionStatus.working,
                                header: SessionViewHeader(session: live),
                                onHeaderVisible: (v) =>
                                    _headerVisible.value = v,
                              ),
                            },
                          ),
                          // Not tied to the composer: a dead session's tasks
                          // and changes stay one tap away.
                          Positioned(
                            left: 0,
                            right: 0,
                            bottom: 0,
                            child: SessionChips(
                              session: live,
                              workspaceId: hasWorkspace ? wsId : null,
                              bottom: !_gone &&
                                      live.status != SessionStatus.dead
                                  ? 0
                                  : 6,
                              onHeight: (h) {
                                if (mounted) setState(() => _chipsHeight = h);
                              },
                            ),
                          ),
                        ],
                      ),
                    ),
                    if (!_gone && live.status != SessionStatus.dead)
                      ConstrainedBox(
                        constraints: BoxConstraints(maxHeight: box.maxHeight),
                        child: SessionComposer(
                            session: live,
                            inputNotice: inputNotice,
                            queue: st.queue,
                            onSent: () => _feedKey.currentState?.follow()),
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
      color: failed ? context.tokens.errorSurface : context.tokens.blockedSurface,
      padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 12),
      child: Text(text,
          style: TextStyle(
              color: failed ? context.tokens.error : context.tokens.textSecondary,
              fontSize: 12)),
    );
  }
}

class _StartingNotice extends StatelessWidget {
  const _StartingNotice();

  @override
  Widget build(BuildContext context) {
    return Center(
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 32),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            Icon(Icons.hourglass_empty,
                size: 48, color: context.tokens.textSecondary),
            const SizedBox(height: 16),
            const Text(
              'Session is starting or waiting at a startup prompt.',
              textAlign: TextAlign.center,
              style: TextStyle(fontWeight: FontWeight.w600),
            ),
            const SizedBox(height: 8),
            Text(
              'Tap the terminal icon to open the live screen and continue.',
              textAlign: TextAlign.center,
              style: TextStyle(color: context.tokens.textSecondary),
            ),
          ],
        ),
      ),
    );
  }
}
