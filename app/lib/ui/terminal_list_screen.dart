import 'dart:math';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../models/terminal.dart';
import '../state/control.dart';
import '../state/gateway.dart';
import '../state/grouping.dart';
import '../state/projects_api.dart';
import '../state/terminals.dart';
import '../state/terminals_api.dart';
import 'live_screen_screen.dart';
import 'node_header.dart';
import 'project_actions.dart';
import 'shell_drawer.dart';
import 'theme.dart';

class TerminalListScreen extends ConsumerWidget {
  const TerminalListScreen({super.key});

  Future<void> _refresh(WidgetRef ref) => ref
      .read(terminalsProvider.notifier)
      .load(ref.read(gatewayProvider)?.client);

  void _say(BuildContext context, String text) => ScaffoldMessenger.of(context)
    ..hideCurrentSnackBar()
    ..showSnackBar(SnackBar(content: Text(text)));

  void _open(BuildContext context, NodeTerminal t) => Navigator.of(context)
      .push(MaterialPageRoute(builder: (_) => LiveScreenScreen(terminal: t)));

  Future<void> _create(BuildContext context, WidgetRef ref) async {
    final nodes = switch (await ref.read(sessionRepositoryProvider).nodes()) {
      Ok(:final value) => [for (final n in value) if (n.terminalSupported) n],
      Error() => const <NodeRef>[],
    };
    if (!context.mounted) return;
    String? nodeId;
    if (nodes.length > 1) {
      nodeId = await showDialog<String>(
        context: context,
        builder: (ctx) => SimpleDialog(
          title: const Text('New terminal on'),
          children: [
            for (final n in nodes)
              SimpleDialogOption(
                onPressed: () => Navigator.of(ctx).pop(n.id),
                child: Text(n.label),
              ),
          ],
        ),
      );
      if (nodeId == null) return;
    } else if (nodes.length == 1) {
      nodeId = nodes.single.id;
    }
    try {
      final t = await ref.read(terminalsApiProvider).create(nodeId);
      if (context.mounted) _open(context, t);
    } catch (e) {
      if (context.mounted) _say(context, 'new terminal failed: ${actionError(e)}');
    }
  }

  Future<void> _actions(BuildContext context, WidgetRef ref, NodeTerminal t) async {
    final action = await showModalBottomSheet<String>(
      context: context,
      useRootNavigator: true,
      builder: (ctx) => SafeArea(
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            ListTile(
              title: Text(t.title,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: const TextStyle(fontWeight: FontWeight.w600)),
            ),
            ListTile(
              leading: const Icon(Icons.edit_outlined),
              title: const Text('Rename'),
              onTap: () => Navigator.of(ctx).pop('rename'),
            ),
            ListTile(
              leading: const Icon(Icons.close, color: AppColors.error),
              title: const Text('Kill', style: TextStyle(color: AppColors.error)),
              onTap: () => Navigator.of(ctx).pop('kill'),
            ),
          ],
        ),
      ),
    );
    if (!context.mounted || action == null) return;
    final api = ref.read(terminalsApiProvider);
    try {
      if (action == 'rename') {
        final name = await showDialog<String>(
          context: context,
          builder: (_) => RenameDialog(current: t.name, title: 'Rename terminal'),
        );
        if (name != null) await api.rename(t.id, name);
      } else {
        final ok = await showDialog<bool>(
          context: context,
          builder: (ctx) => AlertDialog(
            title: Text('Kill terminal ${t.title}?'),
            content: const Text('Its shell stops.'),
            actions: [
              TextButton(
                onPressed: () => Navigator.of(ctx).pop(false),
                child: const Text('Cancel'),
              ),
              FilledButton(
                style: FilledButton.styleFrom(backgroundColor: AppColors.error),
                onPressed: () => Navigator.of(ctx).pop(true),
                child: const Text('Kill'),
              ),
            ],
          ),
        );
        if (ok == true) await api.kill(t.id);
      }
    } catch (e) {
      if (context.mounted) _say(context, '$action failed: ${actionError(e)}');
      // A failed action can mean the terminal is gone, so the list reloads.
      await _refresh(ref);
    }
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final state = ref.watch(terminalsProvider);
    final terms = state.terminals;
    final capable = ref.watch(serverInfoProvider).value?.nodes.where((n) => n.terminalSupported).length ?? 0;
    final grouped = max(terms.map((t) => t.nodeId).toSet().length, capable) > 1;
    final rows = <Widget>[];
    String? node;
    for (final t in terms) {
      if (grouped && t.nodeId != node) {
        node = t.nodeId;
        rows.add(Padding(
          padding: const EdgeInsets.fromLTRB(16, 16, 16, 4),
          child: NodeHeader(label: t.nodeLabel.isEmpty ? t.nodeId : t.nodeLabel),
        ));
      }
      rows.add(ListTile(
        titleAlignment: ListTileTitleAlignment.titleHeight,
        leading: const Icon(Icons.terminal),
        title: Text(t.title),
        subtitle: Text(t.cwd),
        trailing: t.attached ? const Icon(Icons.visibility_outlined, size: 18) : null,
        onTap: () => _open(context, t),
        onLongPress: () => _actions(context, ref, t),
      ));
    }
    return Scaffold(
      appBar: AppBar(
        leading: shellMenuButton(context),
        title: const Text('Terminals'),
      ),
      floatingActionButton: FloatingActionButton.extended(
        onPressed: () => _create(context, ref),
        icon: const Icon(Icons.add),
        label: const Text('New terminal'),
      ),
      body: RefreshIndicator(
        onRefresh: () => _refresh(ref),
        child: terms.isEmpty
            ? ListView(children: [
                Padding(
                  padding: const EdgeInsets.all(32),
                  child: Center(
                    child: !state.loaded && state.error == null
                        ? const CircularProgressIndicator(key: Key('terminals-loading'))
                        : Text(
                            state.error != null
                                ? 'Could not load terminals: ${state.error}'
                                : 'No terminals.',
                            style: state.error != null
                                ? const TextStyle(color: AppColors.error)
                                : null,
                          ),
                  ),
                ),
              ])
            : ListView(children: rows),
      ),
    );
  }
}
