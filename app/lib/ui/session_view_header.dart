import 'package:flutter/material.dart';

import '../models/enums.dart';
import '../models/session.dart';
import 'details_header.dart';
import 'session_card.dart';
import 'status_style.dart';
import 'theme.dart';

/// The session view's large header, at the top of the transcript: the status
/// mark and the repo, the session name, then branch, model, and context. An
/// unnamed session shows its repo as the title on the first line.
class SessionViewHeader extends StatelessWidget {
  const SessionViewHeader({super.key, required this.session, this.wrap = false});

  final Session session;

  /// The details sheet form; see [DetailsHeader.wrap].
  final bool wrap;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = session;
    final name = s.displayName;
    return DetailsHeader(
      mark: Text(
        statusGlyph(s.status),
        style: mono.copyWith(color: sessionColor(s, t)),
      ),
      // An unnamed session shows its repo as the title on the first line.
      label: name == null ? null : s.displayTitle,
      title: name ?? s.displayTitle,
      node: s.nodeLabel,
      meta: [
        if (s.branch?.isNotEmpty ?? false) ('⎇ ${s.branch}', t.textDim),
        ...sessionMetaParts(s, t, agent: false),
        if (s.frontend != FrontendKind.tmux) (s.frontend.name, t.textDim),
      ],
      wrap: wrap,
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
