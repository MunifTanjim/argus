import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../models/enums.dart';
import '../models/session.dart';
import '../state/gateway.dart';
import '../state/grouping.dart';
import '../state/sessions.dart';
import '../transport/connection.dart';
import 'responsive.dart';
import 'session_card.dart';
import 'session_detail_screen.dart';
import 'spawn_dialog.dart';
import 'theme.dart';

class SessionListScreen extends ConsumerWidget {
  const SessionListScreen({super.key});

  Widget _buildCard(
    BuildContext context,
    WidgetRef ref,
    Session s,
    SessionSection section,
    bool grouped,
    bool multiAgent,
  ) {
    final card = SessionCard(
      session: s,
      showNode: section.needsYou && grouped,
      showAgent: multiAgent,
      onTap: () => Navigator.of(context).push(
        MaterialPageRoute(builder: (_) => SessionDetailScreen(session: s)),
      ),
    );
    final canDismiss = s.agent == 'opencode' &&
        !s.controllable &&
        s.status != SessionStatus.working &&
        (s.interaction == null ||
            s.interaction!.kind == InteractionKind.idle);
    if (!canDismiss) return card;
    return Dismissible(
      key: ValueKey(s.id),
      direction: DismissDirection.endToStart,
      background: Container(
        alignment: Alignment.centerRight,
        color: Colors.red,
        padding: const EdgeInsets.only(right: 16),
        child: const Icon(Icons.close, color: Colors.white),
      ),
      onDismissed: (_) => _dismiss(context, ref, s),
      child: card,
    );
  }

  Future<void> _dismiss(BuildContext context, WidgetRef ref, Session s) async {
    ref.read(sessionsProvider.notifier).remove(s.id);
    final result = await ref.read(sessionRepositoryProvider).kill(s.id);
    if (result case Error(:final error)) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (!context.mounted) return;
        ref.read(sessionsProvider.notifier).put(s);
        ScaffoldMessenger.of(context).showSnackBar(
          SnackBar(content: Text('Failed to dismiss: $error')),
        );
      });
    }
  }

  Future<void> _refresh(WidgetRef ref) async {
    final client = ref.read(gatewayProvider)?.client;
    if (client == null) return;
    await refreshSessions(client, ref.read(sessionsProvider.notifier));
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final sessions = ref.watch(sessionsProvider).values;
    final sections = buildSections(sessions);
    // When sessions span nodes, the "Needs you" section mixes hosts under one
    // header, so its cards must name their own node.
    final grouped = nodesFromSessions(sessions).isNotEmpty;
    final multiAgent = sessions
            .map((s) => s.agent)
            .where((a) => a.isNotEmpty)
            .toSet()
            .length >
        1;
    final conn = ref.watch(connStateProvider);
    final connError = ref.watch(connErrorProvider);

    return Scaffold(
      appBar: AppBar(title: const Text('Sessions')),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: () => showSpawnDialog(context, ref),
        icon: const Icon(Icons.add),
        label: const Text('New session'),
      ),
      body: Column(
        children: [
          if (conn != ConnState.connected)
            _ReconnectBanner(state: conn, message: connError),
          Expanded(
            child: RefreshIndicator(
              onRefresh: () => _refresh(ref),
              child: sections.isEmpty
                  ? ListView(
                      children: const [
                        SizedBox(height: 120),
                        Center(
                          child: Text(
                            'No sessions.',
                            style: TextStyle(color: AppColors.dim),
                          ),
                        ),
                      ],
                    )
                  : CenteredBody(
                      child: ListView(
                        padding: const EdgeInsets.all(12),
                        children: [
                          for (final section in sections) ...[
                            _SectionHeader(section: section),
                            for (final s in section.sessions)
                              Padding(
                                padding: const EdgeInsets.only(bottom: 8),
                                child: _buildCard(
                                  context,
                                  ref,
                                  s,
                                  section,
                                  grouped,
                                  multiAgent,
                                ),
                              ),
                            const SizedBox(height: 8),
                          ],
                        ],
                      ),
                    ),
            ),
          ),
        ],
      ),
    );
  }
}

class _SectionHeader extends StatelessWidget {
  const _SectionHeader({required this.section});
  final SessionSection section;

  @override
  Widget build(BuildContext context) {
    final color = section.needsYou ? AppColors.accent : AppColors.dim;
    final label = section.offline
        ? '${section.title} (offline)'
        : section.title;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8, top: 4),
      child: Text(
        '▌ ${label.toUpperCase()}',
        style: TextStyle(
          fontFamily: 'monospace',
          fontSize: 12,
          fontWeight: FontWeight.w700,
          color: color,
        ),
      ),
    );
  }
}

class _ReconnectBanner extends StatelessWidget {
  const _ReconnectBanner({required this.state, this.message});
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
      // A fatal error (e.g. changed host key): show the actionable message, not
      // a generic label — this is the moment the pin is protecting the user.
      ConnState.failed => message ?? 'Connection failed',
    };
    return Container(
      width: double.infinity,
      color: failed ? AppColors.errorSurface : AppColors.awaitingSurface,
      padding: const EdgeInsets.symmetric(vertical: 6, horizontal: 12),
      child: Text(
        text,
        style: TextStyle(
          color: failed ? AppColors.error : AppColors.secondary,
          fontSize: 12,
        ),
      ),
    );
  }
}
