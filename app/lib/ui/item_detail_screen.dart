import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../models/chunk.dart';
import '../state/tool_detail.dart';
import 'responsive.dart';
import 'theme.dart';
import 'tool_detail.dart';
import 'tool_registry.dart';

String itemTitle(Item it) {
  switch (it.kind) {
    case ItemKind.tool:
      final meta = toolMeta(it.toolName);
      if (meta?.display.isNotEmpty ?? false) return meta!.display;
      return it.toolName ?? 'Tool';
    case ItemKind.thinking:
      return 'Thinking';
    case ItemKind.text:
      return 'Output';
    case ItemKind.subagent:
      final tm = it.soleSubagent;
      if (tm?.isTeammate ?? false) {
        return tm!.name.isNotEmpty ? tm.name : 'Teammate';
      }
      final meta = toolMeta(it.toolName);
      if (meta?.display.isNotEmpty ?? false) return meta!.display;
      return tm?.type ?? 'Subagent';
    case ItemKind.skill:
      return 'Skill';
    case ItemKind.unknown:
      return 'Detail';
  }
}

/// Full-screen detail for one transcript item. The heavy tool body
/// (input/result) is stripped from the streamed chunk, so a tool item fetches it
/// on demand (sessions.toolDetail) and fills the item before rendering.
class ItemDetailScreen extends ConsumerStatefulWidget {
  const ItemDetailScreen({
    super.key,
    required this.item,
    required this.detailRef,
  });

  final Item item;
  final ToolDetailRef detailRef;

  @override
  ConsumerState<ItemDetailScreen> createState() => _ItemDetailScreenState();
}

class _ItemDetailScreenState extends ConsumerState<ItemDetailScreen> {
  late Item _item = widget.item;
  bool _loading = false;
  Object? _error;

  bool get _needsFetch =>
      (widget.item.kind == ItemKind.tool ||
          widget.item.kind == ItemKind.skill ||
          isAgentRefTool(widget.item.toolName)) &&
      (widget.item.toolId?.isNotEmpty ?? false) &&
      widget.item.toolInput == null &&
      widget.item.result == null;

  @override
  void initState() {
    super.initState();
    if (_needsFetch) {
      _loading = true;
      WidgetsBinding.instance.addPostFrameCallback((_) => _fetch());
    }
  }

  Future<void> _fetch() async {
    final result = await ref
        .read(toolDetailApiProvider)
        .fetch(widget.detailRef, widget.item.toolId!);
    if (!mounted) return;
    setState(() {
      _loading = false;
      switch (result) {
        case Ok(:final value):
          _item = widget.item.withToolBody(value);
        case Error(:final error):
          _error = error;
      }
    });
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(itemTitle(_item))),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : SafeArea(
              top: false,
              child: CenteredBody(
              child: SingleChildScrollView(
                padding: const EdgeInsets.all(12),
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  children: [
                    if (_error != null)
                      Padding(
                        padding: const EdgeInsets.only(bottom: 8),
                        child: Text(
                          'Failed to load: $_error',
                          style: const TextStyle(color: AppColors.dim),
                        ),
                      ),
                    toolDetailBody(_item),
                  ],
                ),
              ),
            ),
          ),
    );
  }
}
