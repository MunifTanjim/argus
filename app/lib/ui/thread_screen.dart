import 'dart:math' as math;

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/bot.dart';
import '../state/bots.dart';
import '../state/projects_api.dart' show actionError;
import '../state/sessions.dart';
import '../state/tool_detail.dart';
import 'entry_row.dart' show DraftActions, OfferActions;
import 'bot_style.dart';
import 'session_detail_screen.dart';
import 'theme.dart';
import 'transcript_feed.dart';

/// Where an entry's tool details are read: its timeline entry id is prefixed
/// `s<index>:` naming the session it came from. The live session that writes
/// that transcript serves them; otherwise the transcript file does.
ToolDetailRef timelineDetailRef(
  String nodeId,
  List<BotTimelineSession> sessions,
  String entryId,
) {
  final m = RegExp(r'^s(\d+):').firstMatch(entryId);
  final i = m == null ? -1 : int.parse(m[1]!);
  if (i < 0 || i >= sessions.length || sessions[i].transcriptPath.isEmpty) {
    return const ToolDetailRef();
  }
  final s = sessions[i];
  final agent = s.cli.isEmpty ? null : s.cli;
  for (var j = sessions.length - 1; j >= 0; j--) {
    if (sessions[j].live && sessions[j].transcriptPath == s.transcriptPath) {
      return ToolDetailRef.live(sessions[j].sessionId, agent: agent);
    }
  }
  return ToolDetailRef.history(
    nodeId: nodeId,
    transcriptPath: s.transcriptPath,
    agent: agent,
  );
}

/// Opens a thread's timeline.
Route<void> threadRoute({
  required String botId,
  required String threadId,
  String title = '',
  String initialText = '',
}) => MaterialPageRoute(
  builder: (_) => ThreadScreen(
    botId: botId,
    threadId: threadId,
    title: title,
    initialText: initialText,
  ),
);

/// A thread's timeline: its sessions' transcripts with Argus's events between,
/// a composer that sends to the thread, and a way into its live session.
class ThreadScreen extends ConsumerStatefulWidget {
  const ThreadScreen({
    super.key,
    required this.botId,
    required this.threadId,
    this.title = '',
    this.initialText = '',
  });

  final String botId;
  final String threadId;
  final String title;
  final String initialText;

  @override
  ConsumerState<ThreadScreen> createState() => _ThreadScreenState();
}

class _ThreadScreenState extends ConsumerState<ThreadScreen> {
  late final TextEditingController _text = TextEditingController(
    text: widget.initialText,
  );
  BotTimeline? _timeline;
  Object? _error;
  int _readPos = 0;
  bool _sending = false;

  @override
  void initState() {
    super.initState();
    _load();
  }

  @override
  void dispose() {
    _text.dispose();
    super.dispose();
  }

  Future<void> _load() async {
    final api = ref.read(botsApiProvider);
    try {
      final tl = await api.timeline(widget.threadId);
      if (!mounted) return;
      setState(() {
        _timeline = tl;
        _error = null;
      });
      await _markRead();
    } catch (e) {
      if (mounted) setState(() => _error = e);
    }
  }

  BotThread? get _thread {
    final threads = ref.read(botsProvider).threads[widget.botId];
    for (final t in threads ?? const <BotThread>[]) {
      if (t.id == widget.threadId) return t;
    }
    return null;
  }

  /// Marks the thread read up to its last activity (a turn ends after its last
  /// entry) while the node reports it unread and this screen is on top.
  Future<void> _markRead() async {
    final tl = _timeline;
    if (!mounted || tl == null) return;
    if (!(ModalRoute.of(context)?.isCurrent ?? true)) return;
    final t = _thread;
    if (t != null && !t.unread) return;
    final pos = math.max(t?.lastActiveNanos ?? 0, lastEntryNanos(tl.entries));
    if (pos <= _readPos) return;
    _readPos = pos;
    try {
      await ref
          .read(botsApiProvider)
          .markRead(widget.threadId, position: pos);
    } catch (_) {}
  }

  void _say(String text) => ScaffoldMessenger.of(context)
    ..hideCurrentSnackBar()
    ..showSnackBar(SnackBar(content: Text(text)));

  Bot? get _bot {
    for (final a in ref.read(botsProvider).bots) {
      if (a.id == widget.botId) return a;
    }
    return null;
  }

  Future<void> _send() async {
    final text = _text.text.trim();
    if (text.isEmpty || _sending) return;
    if (_bot?.unreachable ?? false) {
      _say(botOffline);
      return;
    }
    setState(() => _sending = true);
    try {
      await ref
          .read(botsApiProvider)
          .send(widget.botId, text, threadId: widget.threadId);
      _text.clear();
    } catch (e) {
      if (mounted) _say('send: ${actionError(e)}');
    }
    if (mounted) setState(() => _sending = false);
    await _load();
  }

  Future<void> _undo(String commit) async {
    try {
      await ref
          .read(botsApiProvider)
          .memoryUndo(widget.botId, commit, threadId: widget.threadId);
    } catch (e) {
      if (mounted) _say('undo: ${actionError(e)}');
    }
    await _load();
  }

  Future<void> _offer(String offerId, bool start) async {
    final api = ref.read(botsApiProvider);
    try {
      if (start) {
        await api.startOffer(widget.threadId, offerId);
      } else {
        await api.dismissOffer(widget.threadId, offerId);
      }
    } catch (e) {
      if (mounted) _say('${start ? 'start' : 'dismiss'}: ${actionError(e)}');
    }
    await _load();
  }

  Future<void> _draft(int eventId, bool send) async {
    final api = ref.read(botsApiProvider);
    try {
      if (send) {
        await api.sendDraft(widget.threadId, eventId);
      } else {
        await api.discardDraft(widget.threadId, eventId);
      }
    } catch (e) {
      if (mounted) _say('${send ? 'send' : 'discard'}: ${actionError(e)}');
    }
    await _load();
  }

  void _openSession() {
    final id = _timeline == null ? '' : liveSessionOf(_timeline!);
    final session = id.isEmpty ? null : ref.read(sessionsProvider)[id];
    if (session == null) {
      _say('no live session in this thread');
      return;
    }
    Navigator.of(context).push(sessionDetailRoute(session));
  }

  @override
  Widget build(BuildContext context) {
    // A change on any node (a new event, a finished turn) reloads the timeline.
    ref.listen(botsProvider.select((s) => s.bots), (_, _) => _load());
    ref.listen(
      botsProvider.select((s) => s.threads[widget.botId]),
      (_, _) => _markRead(),
    );
    final bot = _bot;
    final tl = _timeline;
    final title = widget.title.isEmpty ? 'untitled' : widget.title;
    final Widget body;
    if (tl == null) {
      body = Center(
        child: _error != null
            ? Text(actionError(_error!), style: const TextStyle(color: AppColors.dim))
            : const CircularProgressIndicator(),
      );
    } else {
      final nodeId = bot?.nodeId ?? '';
      final decided = decidedOffers(tl.entries);
      final draftsDone = decidedDrafts(tl.entries);
      final undone = undoneCommits(tl.entries);
      body = TranscriptFeed(
        detailRef: const ToolDetailRef(),
        detailRefOf: (e) => timelineDetailRef(nodeId, tl.sessions, e.id),
        entries: tl.entries,
        emptyText: 'Empty thread.',
        onUndoMemory: _undo,
        canUndoMemory: (c) => !commitUndone(undone, c),
        draftActions: DraftActions(
          isOpen: (id) => !draftsDone.contains(id),
          onSend: (id) => _draft(id, true),
          onDiscard: (id) => _draft(id, false),
        ),
        offerActions: OfferActions(
          isOpen: (id) => !decided.contains(id),
          onStart: (id) => _offer(id, true),
          onDismiss: (id) => _offer(id, false),
        ),
      );
    }
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(title, maxLines: 1, overflow: TextOverflow.ellipsis),
            if (bot != null)
              Text(
                bot.name,
                style: const TextStyle(fontSize: 12, color: AppColors.dim),
              ),
          ],
        ),
        actions: [
          IconButton(
            tooltip: 'Open session',
            icon: const Icon(Icons.open_in_new),
            onPressed: _openSession,
          ),
        ],
      ),
      body: SafeArea(
        top: false,
        child: Column(
          children: [
            Expanded(child: body),
            _composer(),
          ],
        ),
      ),
    );
  }

  Widget _composer() => Padding(
    padding: const EdgeInsets.fromLTRB(12, 4, 4, 8),
    child: Row(
      children: [
        Expanded(
          child: TextField(
            controller: _text,
            minLines: 1,
            maxLines: 5,
            textInputAction: TextInputAction.newline,
            decoration: const InputDecoration(
              hintText: 'Reply',
              border: InputBorder.none,
            ),
          ),
        ),
        IconButton(
          tooltip: 'Send',
          icon: const Icon(Icons.send),
          onPressed: _sending ? null : _send,
        ),
      ],
    ),
  );
}
