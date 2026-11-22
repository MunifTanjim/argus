import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/pagent.dart';
import '../state/pagents.dart';
import '../state/projects_api.dart' show actionError;
import 'agent_badge.dart';
import 'pagent_style.dart';
import 'theme.dart';

/// Opens an agent's screen: its threads and memory.
Route<void> agentRoute(String agentId) =>
    MaterialPageRoute(builder: (_) => AgentScreen(agentId: agentId));

class AgentScreen extends ConsumerStatefulWidget {
  const AgentScreen({super.key, required this.agentId});

  final String agentId;

  @override
  ConsumerState<AgentScreen> createState() => _AgentScreenState();
}

class _AgentScreenState extends ConsumerState<AgentScreen> {
  PAgent? _agent(PAgentsState s) {
    for (final a in s.agents) {
      if (a.id == widget.agentId) return a;
    }
    return null;
  }

  Future<void> _refresh() async {
    await ref
        .read(pagentsProvider.notifier)
        .load(ref.read(pagentsClientProvider));
    ref.invalidate(agentMemoryUsageProvider(widget.agentId));
  }

  void _say(String text) => ScaffoldMessenger.of(context)
    ..hideCurrentSnackBar()
    ..showSnackBar(SnackBar(content: Text(text)));

  /// Whether an action may run: an unreachable agent's node is not asked.
  bool _reachable(PAgent a) {
    if (!a.unreachable) return true;
    _say(agentOffline);
    return false;
  }

  Future<void> _newChat(PAgent a) async {
    if (!_reachable(a)) return;
    final text = await showDialog<String>(
      context: context,
      builder: (_) => const _NewChatDialog(),
    );
    if (text == null || text.isEmpty || !mounted) return;
    try {
      await ref.read(pagentsApiProvider).send(a.id, text, newThread: true);
    } catch (e) {
      if (mounted) _say('send failed: ${actionError(e)}');
      return;
    }
    await _refresh();
  }

  Future<void> _setPrimary(PAgent a, bool primary) async {
    if (!_reachable(a)) return;
    try {
      await ref.read(pagentsApiProvider).setPrimary(primary ? a.id : null);
    } catch (e) {
      if (mounted) _say('primary: ${actionError(e)}');
    }
    await _refresh();
  }

  @override
  Widget build(BuildContext context) {
    final state = ref.watch(pagentsProvider);
    final agent = _agent(state);
    if (agent == null) {
      return Scaffold(
        appBar: AppBar(title: const Text('Agent')),
        body: const Center(
          child: Text('agent not found', style: TextStyle(color: AppColors.dim)),
        ),
      );
    }
    final primary = state.isPrimary(agent);
    final threads = state.threads[agent.id];
    final usage = ref.watch(agentMemoryUsageProvider(agent.id)).value;
    final label = agentLabel(agent.agent).isNotEmpty
        ? agentLabel(agent.agent)
        : agent.agent;
    final sub = [
      if (label.isNotEmpty) label,
      if (agent.nodeLabel.isNotEmpty) 'home: ${agent.nodeLabel}',
      if (primary) 'primary',
    ].join(' · ');
    return Scaffold(
      appBar: AppBar(
        title: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Text(agent.name),
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
            onPressed: () => _newChat(agent),
          ),
          PopupMenuButton<String>(
            onSelected: (v) => switch (v) {
              'primary' => _setPrimary(agent, !primary),
              _ => null,
            },
            itemBuilder: (_) => [
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
            if (agent.unreachable)
              const _Note(agentOffline)
            else if (threads == null)
              const _Note('loading threads…')
            else if (threads.isEmpty)
              const _Note('no threads · new chat starts one')
            else
              for (final t in threads)
                _ThreadRow(
                  thread: t,
                  onTap: () => _say('thread view comes next'),
                ),
            if (usage != null) ...[
              const SizedBox(height: 16),
              _Heading(
                [
                  'Memory',
                  kbSize(usage.usage) +
                      (usage.budget > 0 ? ' / ${kbSize(usage.budget)}' : ''),
                  if (usage.last != null)
                    'last change ${agoText(usage.last!)}',
                ].join(' · '),
              ),
            ],
          ],
        ),
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
  final PAgentThread thread;
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
  const _NewChatDialog();

  @override
  State<_NewChatDialog> createState() => _NewChatDialogState();
}

class _NewChatDialogState extends State<_NewChatDialog> {
  final _text = TextEditingController();

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
