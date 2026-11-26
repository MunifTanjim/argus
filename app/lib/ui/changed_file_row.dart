import 'package:flutter/material.dart';

import '../models/changes.dart';
import 'theme.dart';

const _mono = TextStyle(fontFamily: monoFamily, fontSize: 13);

({String letter, Color color}) changeStyle(
        String change, ArgusTokens t) => switch (change) {
      'added' => (letter: 'A', color: t.changeAdded),
      'modified' => (letter: 'M', color: t.changeModified),
      'deleted' => (letter: 'D', color: t.changeDeleted),
      'renamed' => (letter: 'R', color: t.changeRenamed),
      'untracked' => (letter: '?', color: t.changeUntracked),
      _ => (letter: '•', color: t.textDim),
    };

class _UnstagedBadge extends StatelessWidget {
  const _UnstagedBadge();

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
      decoration: BoxDecoration(
        border: Border.all(color: t.changeModified),
        borderRadius: BorderRadius.circular(t.radiusSmall),
      ),
      child: Text('unstaged',
          style: TextStyle(
              fontFamily: monoFamily, fontSize: 10, color: t.changeModified)),
    );
  }
}

class ChangedFileRow extends StatelessWidget {
  const ChangedFileRow({super.key, required this.file, required this.onTap});

  final ChangedFile file;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = changeStyle(file.change, t);
    final label =
        file.origPath != null ? '${file.origPath} → ${file.path}' : file.path;
    return InkWell(
      onTap: onTap,
      child: Container(
        margin: const EdgeInsets.only(bottom: 6),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 10),
        decoration: BoxDecoration(
          color: t.surface,
          border: Border.all(color: t.line),
          borderRadius: BorderRadius.circular(t.radius),
        ),
        child: Row(
          children: [
            SizedBox(
              width: 16,
              child: Text(s.letter,
                  style: _mono.copyWith(
                      color: s.color, fontWeight: FontWeight.w700)),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(label,
                  style: _mono.copyWith(color: t.text),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis),
            ),
            // Grouped under "Staged" by net change; flag the extra unstaged edits.
            if (file.staged && file.unstaged) ...[
              const _UnstagedBadge(),
              const SizedBox(width: 6),
            ],
            Icon(Icons.chevron_right, size: 18, color: t.textDim),
          ],
        ),
      ),
    );
  }
}
