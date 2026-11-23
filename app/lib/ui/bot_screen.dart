import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/bot.dart';
import '../state/approval_text.dart';
import '../state/bots.dart';
import '../state/projects_api.dart' show actionError;
import 'agent_badge.dart';
import 'bot_memory_screen.dart';
import 'bot_style.dart';
import 'theme.dart';
import 'thread_screen.dart';

/// Opens a bot's screen: its threads and memory.
Route<void> botRoute(String botId, {String chatText = ''}) =>
    MaterialPageRoute(
      builder: (_) => BotScreen(botId: botId, chatText: chatText),
    );

class BotScreen extends ConsumerStatefulWidget {
  const BotScreen({super.key, required this.botId, this.chatText = ''});

  final String botId;

  /// A message to prefill in a new chat opened with the screen.
  final String chatText;

  @override
  ConsumerState<BotScreen> createState() => _BotScreenState();
}

class _BotScreenState extends ConsumerState<BotScreen> {
  bool _unattended = false;

  @override
  void initState() {
    super.initState();
    if (widget.chatText.isNotEmpty) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        final a = _bot(ref.read(botsProvider));
        if (mounted && a != null) _newChat(a, widget.chatText);
      });
    }
  }

  Bot? _bot(BotsState s) {
    for (final a in s.bots) {
      if (a.id == widget.botId) return a;
    }
    return null;
  }

  Future<void> _refresh() async {
    await ref
        .read(botsProvider.notifier)
        .load(ref.read(botsClientProvider));
    ref.invalidate(botMemoryUsageProvider(widget.botId));
  }

  void _say(String text) => ScaffoldMessenger.of(context)
    ..hideCurrentSnackBar()
    ..showSnackBar(SnackBar(content: Text(text)));

  /// Whether an action may run: an unreachable bot's node is not asked.
  bool _reachable(Bot a) {
    if (!a.unreachable) return true;
    _say(botOffline);
    return false;
  }

  Future<void> _newChat(Bot a, [String initial = '']) async {
    if (!_reachable(a)) return;
    final text = await showDialog<String>(
      context: context,
      builder: (_) => _NewChatDialog(initial: initial),
    );
    if (text == null || text.isEmpty || !mounted) return;
    try {
      final res = await ref
          .read(botsApiProvider)
          .send(a.id, text, newThread: true);
      if (!mounted) return;
      _refresh();
      await Navigator.of(context).push(
        threadRoute(botId: a.id, threadId: res.threadId),
      );
    } catch (e) {
      if (mounted) _say('send failed: ${actionError(e)}');
      return;
    }
    if (mounted) await _refresh();
  }

  Future<void> _routineAction(Bot a, BotRoutine r, String action) async {
    if (!_reachable(a)) return;
    final api = ref.read(botsApiProvider);
    try {
      switch (action) {
        case 'run':
          await api.runRoutine(a.id, r.slug);
          if (mounted) _say('${r.slug}: test run started');
        case 'pause':
          await api.pauseRoutine(a.id, r.slug);
        case 'resume':
          await api.resumeRoutine(a.id, r.slug);
      }
    } catch (e) {
      if (mounted) _say('$action: ${actionError(e)}');
    }
    ref.invalidate(botRoutinesProvider(a.id));
    await _refresh();
  }

  Future<void> _setSetting(Bot a, _SettingSpec spec, String value) async {
    if (!_reachable(a)) return;
    try {
      await ref.read(botsApiProvider).setSetting(a.id, spec.key, value);
    } catch (e) {
      if (mounted) _say('set: ${actionError(e)}');
      return;
    }
    await _refresh();
  }

  void _openMemory(Bot a) {
    if (!_reachable(a)) return;
    Navigator.of(context).push(botMemoryRoute(a.id));
  }

  Future<void> _setPrimary(Bot a, bool primary) async {
    if (!_reachable(a)) return;
    try {
      await ref.read(botsApiProvider).setPrimary(primary ? a.id : null);
    } catch (e) {
      if (mounted) _say('primary: ${actionError(e)}');
    }
    await _refresh();
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(botsProvider);
    final bot = _bot(state);
    if (bot == null) {
      return Scaffold(
        appBar: AppBar(title: const Text('Bot')),
        body: const Center(
          child: Text('bot not found', style: TextStyle(color: AppColors.dim)),
        ),
      );
    }
    final primary = state.isPrimary(bot);
    final threads = state.threads[bot.id];
    final usage = ref.watch(botMemoryUsageProvider(bot.id)).value;
    final label = agentLabel(bot.agent).isNotEmpty
        ? agentLabel(bot.agent)
        : bot.agent;
    final sub = [
      if (label.isNotEmpty) label,
      if (bot.nodeLabel.isNotEmpty) 'home: ${bot.nodeLabel}',
      if (primary) 'primary',
    ].join(' · ');
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(bot.name),
            if (sub.isNotEmpty)
              Text(
                sub,
                style: const TextStyle(fontSize: 12, color: AppColors.dim),
              ),
          ],
        ),
        actions: [
          IconButton(
            tooltip: 'New chat',
            icon: const Icon(Icons.add_comment_outlined),
            onPressed: () => _newChat(bot),
          ),
          PopupMenuButton<String>(
            onSelected: (v) => switch (v) {
              'memory' => _openMemory(bot),
              'primary' => _setPrimary(bot, !primary),
              _ => null,
            },
            itemBuilder: (_) => [
              const PopupMenuItem(value: 'memory', child: Text('Memory')),
              PopupMenuItem(
                value: 'primary',
                child: Text(primary ? 'Unset primary' : 'Make primary'),
              ),
            ],
          ),
        ],
      ),
      body: RefreshIndicator(
        onRefresh: _refresh,
        child: ListView(
          padding: const EdgeInsets.fromLTRB(12, 8, 12, 24),
          children: [
            const _Heading('Threads'),
            if (bot.unreachable)
              const _Note(botOffline)
            else if (threads == null)
              const _Note('loading threads…')
            else if (threads.isEmpty)
              const _Note('no threads · new chat starts one')
            else
              for (final t in threads)
                _ThreadRow(
                  thread: t,
                  onTap: () => Navigator.of(context).push(
                    threadRoute(
                      botId: bot.id,
                      threadId: t.id,
                      title: t.title,
                    ),
                  ),
                ),
            const SizedBox(height: 16),
            const _Heading('Routines'),
            if (bot.unreachable)
              const _Note(botOffline)
            else
              _RoutinesList(
                botId: bot.id,
                onAction: (r, v) => _routineAction(bot, r, v),
              ),
            const SizedBox(height: 16),
            const _Heading('Usage'),
            _UsageLine(botId: bot.id, unreachable: bot.unreachable),
            const SizedBox(height: 16),
            const _Heading('Activity'),
            if (bot.unreachable)
              const _Note(botOffline)
            else ...[
              Align(
                alignment: Alignment.centerLeft,
                child: FilterChip(
                  label: const Text('Unattended only'),
                  selected: _unattended,
                  onSelected: (v) => setState(() => _unattended = v),
                ),
              ),
              _ActivityList(botId: bot.id, unattended: _unattended),
            ],
            const SizedBox(height: 16),
            const _Heading('Settings'),
            for (final spec in _settings)
              _SettingRow(
                spec: spec,
                setting: bot.settings[spec.field],
                onChanged: (v) => _setSetting(bot, spec, v),
              ),
            if (usage != null) ...[
              const SizedBox(height: 16),
              InkWell(
                onTap: () => _openMemory(bot),
                child: _Heading(
                  [
                  'Memory',
                  kbSize(usage.usage) +
                      (usage.budget > 0 ? ' / ${kbSize(usage.budget)}' : ''),
                  if (usage.last != null)
                    'last change ${agoText(usage.last!)}',
                  ].join(' · '),
                ),
              ),
            ],
          ],
        ),
      ),
    );
  }
}

class _ActivityList extends ConsumerWidget {
  const _ActivityList({required this.botId, required this.unattended});

  final String botId;
  final bool unattended;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final res = ref.watch(botActivityProvider((botId, unattended)));
    final list = res.value;
    if (list == null) {
      return res.hasError
          ? const _Note('activity unavailable')
          : const _Note('loading activity…');
    }
    if (list.isEmpty) return const _Note('no activity');
    return Column(
      children: [
        for (final a in list)
          ListTile(
            dense: true,
            contentPadding: const EdgeInsets.symmetric(horizontal: 4),
            title: Text(
              [a.tool, a.target].where((x) => x.isNotEmpty).join(' '),
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
            subtitle: Text(
              [
                '${a.decision} by ${a.decidedBy}',
                if (a.startedBy.isNotEmpty && a.startedBy != 'you')
                  startedByLabel(a.startedBy),
                agoText(a.createdAt),
              ].where((x) => x.isNotEmpty).join(' · '),
            ),
          ),
      ],
    );
  }
}

class _SettingSpec {
  const _SettingSpec(this.key, this.field, this.title, this.options);
  final String key; // the set RPC's key
  final String field; // the key in BotInfo.settings
  final String title;
  final List<(String, String)> options; // value, label
}

const _settings = [
  _SettingSpec('notify', 'notify', 'Notifications', [
    ('all', 'All'),
    ('needs-you', 'Needs you'),
    ('off', 'Off'),
  ]),
  _SettingSpec('proactivity', 'proactivity', 'Proactivity', [
    ('off', 'Off'),
    ('low', 'Low'),
    ('default', 'Default'),
    ('high', 'High'),
  ]),
  _SettingSpec('memory.mode', 'memory_mode', 'Memory', [
    ('auto', 'Auto'),
    ('review', 'Review'),
    ('off', 'Off'),
  ]),
];

/// One bot setting. The selected segment is what the bot's own file sets;
/// a setting left to the default selects nothing and shows the value that
/// applies. Picking the selected value again removes the setting, so the
/// default applies.
class _SettingRow extends StatelessWidget {
  const _SettingRow({
    required this.spec,
    required this.setting,
    required this.onChanged,
  });

  final _SettingSpec spec;
  final BotSetting? setting;
  final void Function(String value) onChanged;

  @override
  Widget build(BuildContext context) {
    final st = setting;
    final explicit = st != null && st.explicit && st.value.isNotEmpty;
    return Padding(
      padding: const EdgeInsets.symmetric(vertical: 4),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(spec.title, style: const TextStyle(color: AppColors.dim)),
              if (st != null && !explicit && st.value.isNotEmpty)
                Text(
                  '  default (${st.value})',
                  style: const TextStyle(color: AppColors.dim, fontSize: 12),
                ),
            ],
          ),
          const SizedBox(height: 4),
          SegmentedButton<String>(
            key: ValueKey('setting-${spec.key}'),
            emptySelectionAllowed: true,
            showSelectedIcon: false,
            segments: [
              for (final (v, label) in spec.options)
                ButtonSegment(value: v, label: Text(label)),
            ],
            selected: explicit ? {st.value} : {},
            onSelectionChanged: (s) => onChanged(s.isEmpty ? '' : s.first),
          ),
        ],
      ),
    );
  }
}

class _UsageLine extends ConsumerWidget {
  const _UsageLine({required this.botId, required this.unreachable});

  final String botId;
  final bool unreachable;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    if (unreachable) return const _Note(botOffline);
    final usage = ref.watch(botUsageProvider(botId));
    final u = usage.value;
    if (u == null) {
      return usage.hasError
          ? const _Note('usage unavailable')
          : const _Note('loading usage…');
    }
    return _Note('Last 7 days · ${usageText(u.total)}');
  }
}

class _RoutinesList extends ConsumerWidget {
  const _RoutinesList({required this.botId, required this.onAction});

  final String botId;
  final void Function(BotRoutine, String) onAction;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final routines = ref.watch(botRoutinesProvider(botId));
    final list = routines.value;
    if (list == null) {
      return routines.hasError
          ? const _Note('no routines')
          : const _Note('loading routines…');
    }
    if (list.isEmpty) return const _Note('no routines');
    return Column(
      children: [
        for (final r in list) _RoutineRow(routine: r, onAction: onAction),
      ],
    );
  }
}

class _RoutineRow extends StatelessWidget {
  const _RoutineRow({required this.routine, required this.onAction});

  final BotRoutine routine;
  final void Function(BotRoutine, String) onAction;

  @override
  Widget build(BuildContext context) {
    final r = routine;
    final lines = [
      if (r.what.isNotEmpty) r.what,
      if (r.starts.isNotEmpty) r.starts.join('; '),
      if (r.state == 'active' && r.nextDue != null)
        'next ${untilText(r.nextDue!)}',
      routineProgress(r),
    ].where((x) => x.isNotEmpty);
    final runnable = r.state == 'active' || r.state == 'paused';
    return ListTile(
      dense: true,
      contentPadding: const EdgeInsets.symmetric(horizontal: 4),
      title: Row(
        children: [
          Flexible(
            child: Text(r.slug, maxLines: 1, overflow: TextOverflow.ellipsis),
          ),
          const SizedBox(width: 8),
          Text(
            routineStateText(r),
            style: TextStyle(
              fontSize: 12,
              color: r.state == 'active' ? AppColors.accent : AppColors.dim,
            ),
          ),
        ],
      ),
      subtitle: lines.isEmpty ? null : Text(lines.join(' · ')),
      trailing: PopupMenuButton<String>(
        tooltip: 'Routine actions',
        onSelected: (v) => onAction(r, v),
        itemBuilder: (_) => [
          if (runnable)
            const PopupMenuItem(value: 'run', child: Text('Run now')),
          if (r.state == 'active')
            const PopupMenuItem(value: 'pause', child: Text('Pause')),
          if (r.state == 'paused')
            const PopupMenuItem(value: 'resume', child: Text('Resume')),
        ],
      ),
    );
  }
}

class _Heading extends StatelessWidget {
  const _Heading(this.text);
  final String text;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 8),
    child: Text(
      text,
      style: const TextStyle(
        color: AppColors.secondary,
        fontWeight: FontWeight.w700,
      ),
    ),
  );
}

class _Note extends StatelessWidget {
  const _Note(this.text);
  final String text;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.symmetric(vertical: 8),
    child: Text(text, style: const TextStyle(color: AppColors.dim)),
  );
}

class _ThreadRow extends StatelessWidget {
  const _ThreadRow({required this.thread, required this.onTap});
  final BotThread thread;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = thread;
    final (glyph, color) = threadGlyph(t);
    final working = t.sessionId.isNotEmpty && t.sessionStatus == 'working';
    final meta = working ? '${t.kind} · working' : '${t.kind} · ${agoText(t.lastActiveAt)}';
    return ListTile(
      dense: true,
      contentPadding: const EdgeInsets.symmetric(horizontal: 4),
      leading: Text(glyph, style: TextStyle(fontFamily: 'monospace', color: color)),
      title: Row(
        children: [
          Flexible(
            child: Text(
              t.title.isEmpty ? 'untitled' : t.title,
              maxLines: 1,
              overflow: TextOverflow.ellipsis,
            ),
          ),
          if (t.unread) const UnreadDot(),
        ],
      ),
      trailing: Text(
        meta,
        style: const TextStyle(color: AppColors.dim, fontSize: 12),
      ),
      onTap: onTap,
    );
  }
}

class _NewChatDialog extends StatefulWidget {
  const _NewChatDialog({this.initial = ''});

  final String initial;

  @override
  State<_NewChatDialog> createState() => _NewChatDialogState();
}

class _NewChatDialogState extends State<_NewChatDialog> {
  late final _text = TextEditingController(text: widget.initial);

  @override
  void dispose() {
    _text.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => AlertDialog(
    title: const Text('New chat'),
    content: TextField(
      controller: _text,
      autofocus: true,
      minLines: 1,
      maxLines: 6,
      decoration: const InputDecoration(hintText: 'Message'),
    ),
    actions: [
      TextButton(
        onPressed: () => Navigator.pop(context),
        child: const Text('Cancel'),
      ),
      FilledButton(
        onPressed: () => Navigator.pop(context, _text.text.trim()),
        child: const Text('Send'),
      ),
    ],
  );
}
