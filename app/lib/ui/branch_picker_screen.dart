import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/project_sources.dart';
import '../state/projects_api.dart';
import 'theme.dart';

const double kBranchRowHeight = 48;

/// A branch list for the create page's Branches tab and the branch picker.
class BranchListView extends StatelessWidget {
  const BranchListView({
    super.key,
    required this.branches,
    required this.filter,
    required this.onPick,
    required this.onRetry,
    this.current = '',
    this.disableInUse = false,
    this.controller,
  });

  final AsyncValue<List<BranchInfo>> branches;
  final String filter;
  final String current;
  final bool disableInUse;
  final ValueChanged<String> onPick;
  final VoidCallback onRetry;
  final ScrollController? controller;

  @override
  Widget build(BuildContext context) => branches.when(
    loading: () => const Center(child: CircularProgressIndicator()),
    error: (e, _) => ListNotice(text: actionError(e), onRetry: onRetry),
    data: (all) {
      if (all.isEmpty) return const ListNotice(text: 'No branches');
      final shown = filterBranches(all, filter);
      if (shown.isEmpty) return const ListNotice(text: 'No matches');
      return ListView.builder(
        controller: controller,
        itemExtent: kBranchRowHeight,
        itemCount: shown.length,
        itemBuilder: (_, i) {
          final b = shown[i];
          final disabled = disableInUse && b.checkedOut;
          return ListTile(
            enabled: !disabled,
            title: Row(
              children: [
                Flexible(
                  child: Text(
                    b.name,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                  ),
                ),
                if (b.remote && !b.local) const _Mark('origin'),
                if (disabled) const _Mark('(in use)'),
                if (b.name == current) const _Mark('(current)'),
              ],
            ),
            onTap: disabled ? null : () => onPick(b.name),
          );
        },
      );
    },
  );
}

class _Mark extends StatelessWidget {
  const _Mark(this.text);
  final String text;

  @override
  Widget build(BuildContext context) => Padding(
    padding: const EdgeInsets.only(left: 8),
    child: Text(
      text,
      style: const TextStyle(color: AppColors.dim, fontSize: 12),
    ),
  );
}

class ListNotice extends StatelessWidget {
  const ListNotice({super.key, required this.text, this.onRetry});
  final String text;
  final VoidCallback? onRetry;

  @override
  Widget build(BuildContext context) => Center(
    child: Column(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          text,
          textAlign: TextAlign.center,
          style: const TextStyle(color: AppColors.dim),
        ),
        if (onRetry != null)
          TextButton(onPressed: onRetry, child: const Text('Retry')),
      ],
    ),
  );
}

class BranchPickerScreen extends ConsumerStatefulWidget {
  const BranchPickerScreen({
    super.key,
    required this.projectId,
    required this.title,
    this.current = '',
  });

  final String projectId;
  final String title;
  final String current;

  @override
  ConsumerState<BranchPickerScreen> createState() => _BranchPickerScreenState();
}

class _BranchPickerScreenState extends ConsumerState<BranchPickerScreen> {
  final _scroll = ScrollController();
  var _filter = '';
  var _scrolled = false;

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
  }

  void _scrollToCurrent(List<BranchInfo> all) {
    if (_scrolled || widget.current.isEmpty) return;
    _scrolled = true;
    final i = all.indexWhere((b) => b.name == widget.current);
    if (i <= 0) return;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!_scroll.hasClients) return;
      final max = _scroll.position.maxScrollExtent;
      _scroll.jumpTo((i * kBranchRowHeight).clamp(0, max));
    });
  }

  @override
  Widget build(BuildContext context) {
    final branches = ref.watch(projectBranchesProvider(widget.projectId));
    if (branches.value case final all?) _scrollToCurrent(all);
    return Scaffold(
      appBar: AppBar(title: Text(widget.title)),
      body: SafeArea(
        top: false,
        child: Column(
          children: [
            Padding(
              padding: const EdgeInsets.all(8),
              child: TextField(
                key: const Key('branch-filter'),
                decoration: const InputDecoration(
                  hintText: 'Filter branches',
                  prefixIcon: Icon(Icons.search),
                ),
                onChanged: (v) => setState(() => _filter = v),
              ),
            ),
            Expanded(
              child: BranchListView(
                branches: branches,
                filter: _filter,
                current: widget.current,
                controller: _scroll,
                onPick: (name) => Navigator.of(context).pop(name),
                onRetry: () =>
                    ref.invalidate(projectBranchesProvider(widget.projectId)),
              ),
            ),
          ],
        ),
      ),
    );
  }
}

Future<String?> pickBranch(
  NavigatorState nav, {
  required String projectId,
  required String title,
  String current = '',
}) => nav.push<String>(
  MaterialPageRoute(
    builder: (_) => BranchPickerScreen(
      projectId: projectId,
      title: title,
      current: current,
    ),
  ),
);
