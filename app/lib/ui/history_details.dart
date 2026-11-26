import 'package:flutter/material.dart';

import '../models/history.dart';
import 'agent_badge.dart';
import 'details_header.dart';
import 'history_format.dart';
import 'theme.dart';
import 'usage_format.dart';

/// A history app bar title that opens details on tap, like the live session's
/// compact title. The trailing `▾` says a tap opens them.
class HistoryTitle extends StatelessWidget {
  const HistoryTitle({super.key, required this.text, required this.onTap});

  final String text;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Align(
      alignment: Alignment.centerLeft,
      child: InkWell(
        key: const Key('history-title'),
        borderRadius: BorderRadius.circular(t.radius),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              Flexible(
                child: Text(text, maxLines: 1, overflow: TextOverflow.ellipsis),
              ),
              const SizedBox(width: 2),
              Icon(Icons.expand_more, size: 18, color: t.textDim),
            ],
          ),
        ),
      ),
    );
  }
}

/// The first non-empty label, or null.
String? firstLabel(Iterable<String?> labels) =>
    labels.firstWhere((l) => l?.isNotEmpty ?? false, orElse: () => null);

/// A past session's details: where it ran, its full title, its size, when it
/// ran, and the first message when a title hides it.
Future<void> showHistorySessionDetails(
  BuildContext context,
  HistorySession s,
  HistoryProject? project,
) {
  final t = context.tokens;
  final label = project?.label ?? '';
  // Colored like the live session header: the agent's brand color and the
  // model's own color; the counts stay dim.
  final meta = <(String, Color)>[
    if (s.agent.isNotEmpty) (agentLabel(s.agent), agentColor(s.agent, t)),
    if (s.modelName?.isNotEmpty ?? false)
      (s.modelName!, modelColor(s.modelColor, t)),
    if (s.turnCount > 0)
      (s.turnCount == 1 ? '1 turn' : '${s.turnCount} turns', t.textDim),
    (compactDuration(s.durationMs), t.textDim),
    if (s.tokens > 0) ('${fmtTokens(s.tokens)} tok', t.textDim),
  ].where((p) => p.$1.isNotEmpty).toList();
  final at = DateTime.tryParse(s.lastActivity)?.toLocal();
  final hasTitle = s.title?.isNotEmpty ?? false;
  final first = s.firstMessage ?? '';
  return showDetailsSheet(
    context,
    (sheet) => Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        DetailsHeader(
          label: label.isEmpty ? null : label,
          title: s.displayTitle,
          node: firstLabel([s.nodeLabel, project?.nodeLabel, s.nodeId]),
          meta: meta,
          wrap: true,
        ),
        if (at != null)
          Text(
            '${dayLabel(at, DateTime.now())} ${clockTime(at)}',
            style: mono.copyWith(color: t.textDim, fontSize: 11),
          ),
        if (hasTitle && first.isNotEmpty) ...[
          const SizedBox(height: 12),
          Text(
            '“$first',
            maxLines: 4,
            overflow: TextOverflow.ellipsis,
            style: TextStyle(color: t.textSecondary),
          ),
        ],
      ],
    ),
  );
}

/// A project's details: its name and machine, size, last activity, and the
/// full path, which a tap copies.
Future<void> showHistoryProjectDetails(BuildContext context, HistoryProject p) {
  final t = context.tokens;
  final n = p.sessionCount;
  final ago = shortAgo(p.lastActivity);
  final meta = <(String, Color)>[
    (n == 1 ? '1 session' : '$n sessions', t.textDim),
    if (ago.isNotEmpty)
      (ago == 'now' ? 'active now' : 'active $ago ago', t.textDim),
    if (p.missing) ('folder missing', t.textDim),
  ];
  return showDetailsSheet(
    context,
    (sheet) => Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        DetailsHeader(
          title: p.label,
          node: firstLabel([p.nodeLabel, p.nodeId]),
          meta: meta,
          wrap: true,
        ),
        if (p.cwd.isNotEmpty) ...[
          const SizedBox(height: 12),
          DetailsPathRow(path: p.cwd, sheet: sheet),
        ],
      ],
    ),
  );
}
