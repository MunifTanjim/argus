import 'package:flutter/material.dart';

import '../models/changes.dart';
import 'theme.dart';

const _mono = TextStyle(fontFamily: 'monospace', fontSize: 13);

({String letter, Color color}) changeStyle(String change) => switch (change) {
      'added' => (letter: 'A', color: const Color(0xFFb8bb26)),
      'modified' => (letter: 'M', color: const Color(0xFFd79921)),
      'deleted' => (letter: 'D', color: const Color(0xFFfb4934)),
      'renamed' => (letter: 'R', color: const Color(0xFFd3869b)),
      'untracked' => (letter: '?', color: const Color(0xFF83a598)),
      _ => (letter: '•', color: AppColors.dim),
    };

class _UnstagedBadge extends StatelessWidget {
  const _UnstagedBadge();

  @override
  Widget build(BuildContext context) => Container(
        padding: const EdgeInsets.symmetric(horizontal: 5, vertical: 1),
        decoration: BoxDecoration(
          border: Border.all(color: const Color(0xFFd79921)),
          borderRadius: BorderRadius.circular(3),
        ),
        child: const Text('unstaged',
            style: TextStyle(
                fontFamily: 'monospace',
                fontSize: 10,
                color: Color(0xFFd79921))),
      );
}

class ChangedFileRow extends StatelessWidget {
  const ChangedFileRow({super.key, required this.file, required this.onTap});

  final ChangedFile file;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final s = changeStyle(file.change);
    final label =
        file.origPath != null ? '${file.origPath} → ${file.path}' : file.path;
    return InkWell(
      onTap: onTap,
      child: Container(
        margin: const EdgeInsets.only(bottom: 6),
        padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 10),
        decoration: BoxDecoration(
          color: AppColors.card,
          border: Border.all(color: AppColors.border),
          borderRadius: BorderRadius.circular(4),
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
                  style: _mono.copyWith(color: AppColors.text),
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis),
            ),
            // Grouped under "Staged" by net change; flag the extra unstaged edits.
            if (file.staged && file.unstaged) ...[
              const _UnstagedBadge(),
              const SizedBox(width: 6),
            ],
            const Icon(Icons.chevron_right, size: 18, color: AppColors.dim),
          ],
        ),
      ),
    );
  }
}
