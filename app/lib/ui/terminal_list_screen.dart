import 'dart:math';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../state/control.dart';
import '../state/grouping.dart';
import '../state/terminals.dart';
import 'node_header.dart';
import 'shell_drawer.dart';
import 'terminal_tile.dart';

class TerminalListScreen extends ConsumerWidget {
  const TerminalListScreen({super.key});

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
    if (context.mounted) await createTerminal(context, ref, nodeId);
  }

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    final terms = ref.watch(terminalsProvider).terminals;
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
      rows.add(TerminalTile(terminal: t));
    }
    return Scaffold(
      appBar: AppBar(
        leading: shellMenuButton(context),
        title: const Text('Terminals'),
      ),
      floatingActionButton: FloatingActionButton(
        heroTag: null,
        tooltip: 'New terminal',
        onPressed: () => _create(context, ref),
        child: const Icon(Icons.add),
      ),
      body: TerminalListBody(children: rows),
    );
  }
}
