import 'package:flutter/material.dart';

import '../models/enums.dart';
import '../models/session.dart';
import 'details_header.dart';
import 'session_card.dart';
import 'status_style.dart';
import 'theme.dart';
import 'top_bar.dart';

/// The session view's large header, at the top of the transcript: the status
/// mark and the repo, the session name, then branch, model, and context. An
/// unnamed session shows its repo as the title on the first line.
class SessionViewHeader extends StatelessWidget {
  const SessionViewHeader({
    super.key,
    required this.session,
    this.compact = false,
  });

  final Session session;

  /// The in-feed form; see [DetailsHeader.compact].
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = session;
    final name = s.displayName;
    return DetailsHeader(
      mark: StatusMark(
        status: s.status,
        color: sessionColor(s, t),
        fontSize: 12,
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
      compact: compact,
    );
  }
}

/// The app bar's one-line title: the status mark and the session name, or the
/// repo for an unnamed session. A tap opens the details.
class SessionTitle extends StatelessWidget {
  const SessionTitle({super.key, required this.session, required this.onTap});

  final Session session;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) => TopBarTitle(
    leading: StatusMark(
      status: session.status,
      color: sessionColor(session, context.tokens),
      fontSize: 16,
    ),
    text: session.displayName ?? session.displayTitle,
    onTap: onTap,
  );
}
