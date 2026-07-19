import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../models/changes.dart';
import '../state/changes.dart';
import 'edit_diff.dart';
import 'responsive.dart';
import 'theme.dart';

const _mono = TextStyle(fontFamily: 'monospace', fontSize: 12, height: 1.35);

/// Full-screen review of one changed file as a collapsible unified diff.
class ChangedFileReviewScreen extends ConsumerStatefulWidget {
  const ChangedFileReviewScreen({
    super.key,
    required this.sessionId,
    required this.file,
    this.rev,
  });

  final String sessionId;
  final ChangedFile file;
  final String? rev;

  @override
  ConsumerState<ChangedFileReviewScreen> createState() =>
      _ChangedFileReviewScreenState();
}

class _ChangedFileReviewScreenState
    extends ConsumerState<ChangedFileReviewScreen> {
  bool _loading = true;
  FileDiff? _diff;
  Object? _error;

  @override
  void initState() {
    super.initState();
    WidgetsBinding.instance.addPostFrameCallback((_) => _fetch());
  }

  Future<void> _fetch() async {
    try {
      final diff = await ref.read(changesApiProvider).fileDiff(
            widget.sessionId,
            widget.file.path,
            origPath: widget.file.origPath,
            rev: widget.rev,
          );
      if (!mounted) return;
      setState(() {
        _loading = false;
        _diff = diff;
      });
    } catch (e) {
      if (!mounted) return;
      setState(() {
        _loading = false;
        _error = e;
      });
    }
  }

  String get _fileName => widget.file.path.split('/').last;

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      appBar: AppBar(title: Text(_fileName)),
      body: _loading
          ? const Center(child: CircularProgressIndicator())
          : SafeArea(
              top: false,
              child: CenteredBody(
                child: Padding(
                  padding: const EdgeInsets.all(12),
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.start,
                    children: _body(),
                  ),
                ),
              ),
            ),
    );
  }

  List<Widget> _body() {
    final header = widget.file.origPath != null
        ? '● ${widget.file.origPath} → ${widget.file.path}'
        : '● ${widget.file.path}';
    final blocks = <Widget>[
      Padding(
        padding: const EdgeInsets.only(bottom: 8),
        child: Text(header, style: _mono.copyWith(color: AppColors.dim)),
      ),
    ];

    if (_error != null) {
      blocks.add(Text('Failed to load: $_error',
          style: const TextStyle(color: AppColors.dim)));
      return blocks;
    }
    final d = _diff;
    if (d == null) return blocks;
    if (d.notShown) {
      blocks.add(Text('Not shown — binary or too large.',
          style: _mono.copyWith(color: AppColors.dim)));
      return blocks;
    }
    if (d.oldContent.isEmpty && d.newContent.isEmpty) {
      blocks.add(Text('Empty file — no content to show.',
          style: _mono.copyWith(color: AppColors.dim)));
      return blocks;
    }
    // Flexible (not Expanded) so a short diff stays compact under the path
    // header, while a long one caps at the viewport and scrolls its own content.
    blocks.add(Flexible(
        child: collapsibleDiffView(d.oldContent, d.newContent, lang: d.path)));
    return blocks;
  }
}
