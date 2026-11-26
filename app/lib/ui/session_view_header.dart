import 'package:flutter/material.dart';

import '../models/enums.dart';
import '../models/session.dart';
import 'session_card.dart';
import 'status_style.dart';
import 'theme.dart';

/// The session view's large header, at the top of the transcript: the status
/// mark and the repo, the session name, then branch, model, and context. An
/// unnamed session shows its repo as the title on the first line.
class SessionViewHeader extends StatelessWidget {
  const SessionViewHeader({
    super.key,
    required this.session,
    this.showDivider = true,
  });

  final Session session;

  /// The rule under the header; the details sheet leaves it out.
  final bool showDivider;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = session;
    final name = s.displayName;
    final small = mono.copyWith(color: t.textDim, fontSize: 11);
    final title = mono.copyWith(
      color: t.text,
      fontSize: 17,
      fontWeight: FontWeight.w600,
    );
    final meta = <(String, Color)>[
      if (s.branch?.isNotEmpty ?? false) ('⎇ ${s.branch}', t.textDim),
      ...sessionMetaParts(s, t, agent: false),
      if (s.frontend != FrontendKind.tmux) (s.frontend.name, t.textDim),
    ];
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Row(
          children: [
            Text(
              statusGlyph(s.status),
              style: mono.copyWith(color: sessionColor(s, t)),
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Text(
                s.displayTitle,
                maxLines: 1,
                overflow: TextOverflow.ellipsis,
                style: name == null ? title : small,
              ),
            ),
            if (s.nodeLabel?.isNotEmpty ?? false) ...[
              const SizedBox(width: 8),
              // Gives way to the title, but keeps a short label whole.
              Flexible(
                child: Text(
                  s.nodeLabel!,
                  maxLines: 1,
                  overflow: TextOverflow.ellipsis,
                  style: small,
                ),
              ),
            ],
          ],
        ),
        if (name != null)
          Text(
            name,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: title,
          ),
        if (meta.isNotEmpty) ...[
          const SizedBox(height: 2),
          Text.rich(
            TextSpan(
              style: small,
              children: [
                for (final (i, (text, color)) in meta.indexed) ...[
                  if (i > 0) const TextSpan(text: ' · '),
                  TextSpan(
                    text: text,
                    style: TextStyle(color: color),
                  ),
                ],
              ],
            ),
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
        ],
        if (showDivider) ...[
          const SizedBox(height: 12),
          Divider(height: 1, color: t.line),
        ],
      ],
    );
  }
}

/// The app bar's one-line title: the status mark and the session name, or the
/// repo for an unnamed session. The trailing `▾` says a tap opens the details.
class SessionTitle extends StatelessWidget {
  const SessionTitle({super.key, required this.session});

  final Session session;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = session;
          // In the app bar's title style.
    return Row(
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          statusGlyph(s.status),
          style: mono.copyWith(fontSize: 16, color: sessionColor(s, t)),
        ),
        const SizedBox(width: 8),
        Flexible(
          // In the app bar's title style.
          child: Text(
            s.displayName ?? s.displayTitle,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
        ),
        const SizedBox(width: 2),
        Icon(Icons.expand_more, size: 18, color: t.textDim),
      ],
    );
  }
}
