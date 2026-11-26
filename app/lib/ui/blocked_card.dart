import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/enums.dart';
import '../models/session.dart';
import 'card_layout.dart';
import 'relative_time.dart';
import 'session_actions.dart';
import 'session_card.dart';
import 'session_composer.dart';
import 'session_detail_screen.dart';
import 'theme.dart';

/// A blocked session on home: its prompt and the command it asks to run. A
/// tap opens the session, where the prompt is answered.
class BlockedCard extends StatelessWidget {
  const BlockedCard({super.key, required this.session, this.showNode = false});

  final Session session;

  /// Show the origin node, as in the cross-host "Needs you" section.
  final bool showNode;

  void _open(BuildContext context) =>
      Navigator.of(context).push(sessionDetailRoute(session));

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final s = session;
    final ix = s.interaction!;
    final time = relativeTime(
      s.summary?.lastActivity ?? s.statusSince?.toIso8601String(),
    );
    final radius = BorderRadius.circular(t.radius + 4);
    // The command a permission asks to run, or the start of a plan.
    final preview = _previewOf(ix);
    final question =
        ix.kind == InteractionKind.question && ix.questions.isNotEmpty
        ? ix.questions.first
        : null;

    final body = Padding(
      padding: const EdgeInsets.fromLTRB(kCardInset, 12, 12, 12),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SessionHeader(
            session: s,
            mark: Text(
              '●',
              textAlign: TextAlign.center,
              style: mono.copyWith(color: t.blocked),
            ),
            style: mono.copyWith(
              color: t.text,
              fontSize: 13,
              fontWeight: FontWeight.w600,
            ),
            trailing: [
              if (showNode) ...[
                const SizedBox(width: 8),
                Icon(Icons.dns_outlined, size: 13, color: t.textDim),
                const SizedBox(width: 4),
                Text(
                  s.nodeLabel ?? s.nodeId ?? 'local',
                  style: mono.copyWith(color: t.textDim, fontSize: 11),
                ),
              ],
              if (time.isNotEmpty) ...[
                const SizedBox(width: 8),
                Text(
                  time,
                  style: mono.copyWith(color: t.textDim, fontSize: 11),
                ),
              ],
            ],
          ),
          const SizedBox(height: 6),
          _KindLine(interaction: ix),
          if ((ix.message ?? '').isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(
              ix.message!,
              maxLines: 4,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(color: t.textSecondary, fontSize: 13),
            ),
          ],
          if ((question?.question ?? '').isNotEmpty) ...[
            const SizedBox(height: 6),
            Text(
              question!.question!,
              maxLines: 2,
              overflow: TextOverflow.ellipsis,
              style: TextStyle(color: t.text, fontSize: 13),
            ),
          ],
          if (question != null && question.options.isNotEmpty) ...[
            const SizedBox(height: 8),
            _OptionChips(options: question.options),
          ],
          if (preview != null) ...[
            const SizedBox(height: 8),
            Container(
              width: double.infinity,
              padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
              decoration: BoxDecoration(
                color: t.surface,
                borderRadius: BorderRadius.circular(t.radius),
              ),
              child: Text(
                preview,
                maxLines: 3,
                overflow: TextOverflow.ellipsis,
                style: mono.copyWith(color: t.text, fontSize: 11),
              ),
            ),
          ],
        ],
      ),
    );

    return Opacity(
      opacity: s.offline ? 0.5 : 1,
      child: Material(
        color: Color.alphaBlend(t.blocked.withValues(alpha: 0.08), t.canvas),
        shape: RoundedRectangleBorder(
          borderRadius: radius,
          side: BorderSide(color: t.blocked.withValues(alpha: 0.5)),
        ),
        clipBehavior: Clip.antiAlias,
        child: InkWell(
          onTap: () => _open(context),
          onLongPress: () => showSessionSheet(context, s),
          child: body,
        ),
      ),
    );
  }
}

/// What the card's prompt is: an icon and a label in the blocked color, the
/// same label as the composer's status strip.
class _KindLine extends StatelessWidget {
  const _KindLine({required this.interaction});

  final Interaction interaction;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    final ix = interaction;
    final n = ix.questions.length;
    final icon = switch (ix.kind) {
      InteractionKind.permission => Icons.shield_outlined,
      InteractionKind.question => Icons.help_outline,
      InteractionKind.plan => Icons.description_outlined,
      _ => Icons.notifications_none,
    };
    final label = [
      interactionLabel(ix),
      if (ix.kind == InteractionKind.question && n > 1) '1 of $n',
    ].join(' · ');
    return Row(
      children: [
        Icon(icon, size: 14, color: t.blocked),
        const SizedBox(width: 6),
        Expanded(
          child: Text(
            label,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
            style: mono.copyWith(color: t.blocked, fontSize: 11),
          ),
        ),
      ],
    );
  }
}

/// A question's options as plain chips: the first three, then "+N". Not
/// buttons; a tap on the card opens the session.
class _OptionChips extends StatelessWidget {
  const _OptionChips({required this.options});

  static const _shown = 3;

  final List<String> options;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    Widget chip(String text) => Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        border: Border.all(color: t.line),
        borderRadius: BorderRadius.circular(10),
      ),
      child: Text(
        text,
        maxLines: 1,
        overflow: TextOverflow.ellipsis,
        style: TextStyle(color: t.textSecondary, fontSize: 12),
      ),
    );
    return Wrap(
      spacing: 6,
      runSpacing: 6,
      children: [
        for (final o in options.take(_shown)) chip(o),
        if (options.length > _shown) chip('+${options.length - _shown}'),
      ],
    );
  }
}

// An interaction is immutable, and its tool input can hold a whole file, so
// the preview is computed once per interaction, not on every rebuild.
final _previews = Expando<(String?,)>();

/// The command a permission asks to run, or the start of a plan, cut to a
/// length that fills the card's three lines.
String? _previewOf(Interaction ix) => (_previews[ix] ??= (
  switch (ix.kind) {
    InteractionKind.permission => _clip(_readableInput(ix.toolInput)),
    InteractionKind.plan => _planPreview(ix.plan),
    _ => null,
  },
)).$1;

String? _clip(String? s) =>
    s == null || s.length <= 1000 ? s : s.substring(0, 1000);

/// The first three non-blank lines of [plan], as plain text.
String? _planPreview(String? plan) {
  if (plan == null) return null;
  final lines = plan
      .split('\n')
      .map((l) => l.trimRight())
      .where((l) => l.trim().isNotEmpty)
      .take(3);
  return lines.isEmpty ? null : lines.join('\n');
}

/// [raw] in a short readable form: the `command` of a Bash-like JSON input,
/// otherwise [raw] itself.
String? _readableInput(String? raw) {
  if (raw == null || raw.isEmpty) return null;
  try {
    final j = jsonDecode(raw);
    if (j is Map && j['command'] is String) return j['command'] as String;
  } on FormatException {
    // Not JSON: show it as is.
  }
  return raw;
}
