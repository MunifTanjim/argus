import 'package:flutter/material.dart';

import '../models/enums.dart';
import '../models/session.dart';
import 'status_style.dart';
import 'theme.dart';

class SessionCard extends StatelessWidget {
  const SessionCard({super.key, required this.session, this.onTap});

  final Session session;
  final VoidCallback? onTap;

  static const _mono = TextStyle(fontFamily: 'monospace', fontSize: 12);

  @override
  Widget build(BuildContext context) {
    final s = session;
    final awaiting = s.status == SessionStatus.awaitingInput;
    final title = s.displayTitle;

    final meta = <String>[
      if (s.summary?.model != null) s.summary!.model!,
      if (s.summary?.hasContext ?? false)
        '${s.summary!.contextPct.round()}% ctx',
      if ((s.summary?.tokens ?? 0) > 0) '${s.summary!.tokens} tok',
    ].join('  ·  ');

    final card = Container(
      decoration: BoxDecoration(
        color: awaiting ? AppColors.awaitingSurface : AppColors.card,
        border: Border.all(
            color: awaiting ? AppColors.awaitingBorder : AppColors.border),
        borderRadius: BorderRadius.circular(6),
      ),
      padding: const EdgeInsets.all(12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          Row(
            children: [
              Text(statusGlyph(s.status),
                  style: TextStyle(
                      fontFamily: 'monospace', color: statusColor(s.status))),
              const SizedBox(width: 8),
              Expanded(
                child: Text(title,
                    maxLines: 1,
                    overflow: TextOverflow.ellipsis,
                    style: const TextStyle(
                        color: AppColors.text, fontWeight: FontWeight.w600)),
              ),
            ],
          ),
          if ((s.summary?.task ?? s.interaction?.message) != null) ...[
            const SizedBox(height: 4),
            Text(s.summary?.task ?? s.interaction!.message!,
                maxLines: 2,
                overflow: TextOverflow.ellipsis,
                style: const TextStyle(color: AppColors.secondary, fontSize: 13)),
          ],
          if (meta.isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(meta, style: _mono.copyWith(color: AppColors.dim)),
          ],
        ],
      ),
    );

    return Opacity(
      opacity: s.offline ? 0.5 : 1,
      child: Material(
        color: Colors.transparent,
        child: InkWell(
          borderRadius: BorderRadius.circular(6),
          onTap: onTap,
          child: card,
        ),
      ),
    );
  }
}
