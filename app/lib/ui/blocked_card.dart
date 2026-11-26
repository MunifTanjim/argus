import 'dart:convert';

import 'package:flutter/material.dart';

import '../models/enums.dart';
import '../models/session.dart';
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
    // Filled, the card is tinted and bordered; otherwise it is a plain card
    // with the same stripe or dot as a session card.
    final filled = t.statusStyle == StatusStyle.filled;
    // The command a permission asks to run, or the start of a plan.
    final preview = _previewOf(ix);
    final question =
        ix.kind == InteractionKind.question && ix.questions.isNotEmpty
        ? ix.questions.first
        : null;
    final message = _message(ix, question);

    return StatusCardFrame(
      session: s,
      color: t.blocked,
      surface: filled ? t.blockedSurface : t.surface,
      shape: filled
          ? RoundedRectangleBorder(
              borderRadius: BorderRadius.circular(t.radiusLarge),
              side: BorderSide(color: t.blockedBorder),
            )
          : null,
      onTap: () => _open(context),
      onLongPress: () => showSessionSheet(context, s),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SessionHeader(
            session: s,
            mark: StatusMark(status: s.status, color: t.blocked),
            style: mono.copyWith(
              color: t.text,
              fontSize: 13,
              fontWeight: FontWeight.w600,
            ),
            trailing: [
              ...cardTime(s, t),
              if (showNode) ...cardNode(s, t),
            ],
          ),
          const SizedBox(height: 6),
          _KindLine(interaction: ix),
          if (message != null) ...[
            const SizedBox(height: 6),
            Text(
              message,
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
                // Set off from the card behind it.
                color: filled ? t.surface : t.canvas,
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
        borderRadius: BorderRadius.circular(t.radius),
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

/// [ix]'s message, or null when it is blank or, on a question, repeats the
/// kind line ("Questions" from an OpenCode form title) or [question]'s text.
String? _message(Interaction ix, QuestionSpec? question) {
  final m = ix.message;
  if (m == null || m.trim().isEmpty) return null;
  if (ix.kind != InteractionKind.question) return m;
  String norm(String s) => s.trim().toLowerCase();
  final label = norm(interactionLabel(ix));
  final repeats = {
    label,
    '${label}s',
    if (question?.question != null) norm(question!.question!),
  };
  return repeats.contains(norm(m)) ? null : m;
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
