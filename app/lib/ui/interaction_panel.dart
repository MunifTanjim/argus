import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/command.dart';
import '../core/result.dart';
import '../data/session_repository.dart';
import '../models/entry.dart';
import '../models/enums.dart';
import '../models/session.dart';
import '../state/respond_params.dart';
import '../state/approval_text.dart';
import '../state/projects_api.dart' show actionError;
import '../state/respond_view_model.dart';
import 'live_screen_screen.dart';
import 'code_block.dart';
import 'theme.dart';
import 'tool_detail.dart';
import 'tool_registry.dart';

/// The pending permission, plan or question of [session], answered inline.
class InteractionPanel extends ConsumerStatefulWidget {
  const InteractionPanel({super.key, required this.session});
  final Session session;

  @override
  ConsumerState<InteractionPanel> createState() => _InteractionPanelState();
}

class _InteractionPanelState extends ConsumerState<InteractionPanel> {
  late final RespondViewModel _vm;
  bool _denying = false;
  // A handoff the user took over: Done is the next step, so it is highlighted.
  bool _tookOver = false;
  final _text = TextEditingController();
  final _secret = TextEditingController();
  List<QuestionDraft>? _drafts;

  @override
  void initState() {
    super.initState();
    _vm = RespondViewModel(ref.read(sessionRepositoryProvider));
    _vm.respond.addListener(_onCommand);
  }

  void _onCommand() {
    if (mounted) setState(() {}); // reflect running state on the buttons
  }

  List<QuestionDraft> _ensureDrafts(int n) {
    final d = _drafts;
    if (d != null && d.length == n) return d;
    return _drafts = List.generate(n, (_) => QuestionDraft());
  }

  @override
  void dispose() {
    _vm.respond.removeListener(_onCommand);
    _vm.dispose();
    _text.dispose();
    _secret.clear();
    _secret.dispose();
    super.dispose();
  }

  String get _sid => widget.session.id;
  bool get _busy => _vm.running;

  Future<void> _respond(Map<String, dynamic> params) =>
      _finish(_vm.respond, () => _vm.respond.execute(params));

  Future<void> _finish(
    Command<void> cmd,
    Future<void> Function() exec,
  ) async {
    await exec();
    if (!mounted) return;
    switch (cmd.result) {
      case Ok():
        setState(() => _denying = false);
        _text.clear();
        _secret.clear();
      case Error(:final error):
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed: ${actionError(error)}')));
      case null:
        break;
    }
  }

  @override
  Widget build(BuildContext context) {
    final ix = widget.session.interaction;
    if (ix == null) return const SizedBox.shrink();
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 8, 16, 8),
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: _body(ix),
      ),
    );
  }

  List<Widget> _body(Interaction ix) {
    switch (ix.kind) {
      case InteractionKind.permission:
        return _serverDecision(ix, 'permission');
      case InteractionKind.plan:
        return _serverDecision(ix, 'plan');
      case InteractionKind.idle:
        return const [];
      case InteractionKind.handoff:
        return _handoff(ix);
      case InteractionKind.secret:
        return _secretRequest(ix);
      case InteractionKind.question:
        return _questions(ix);
      case InteractionKind.unknown:
        return [const Text('Unsupported interaction')];
    }
  }

  /// Renders server-built decision options: one button per option, the reject
  /// choice opens a free-text field prompted by its placeholder. The client only
  /// echoes the chosen value back.
  List<Widget> _serverDecision(Interaction ix, String kind) {
    final tool = ix.toolName ?? '';
    final display = toolMeta(widget.session.agent, tool)?.display ?? '';
    final toolLabel = display.isEmpty ? tool : display;
    final heading = kind == 'plan'
        ? 'Plan review'
        : (tool.isEmpty
            ? 'Permission requested'
            : 'Permission requested · $toolLabel');

    final detail = <Widget>[
      Text(heading,
          style: const TextStyle(
              color: AppColors.secondary, fontWeight: FontWeight.w700)),
      for (final w in classWarnings(ix.classes))
        Padding(
          padding: const EdgeInsets.only(top: 4),
          child: Text('⚠ $w',
              style: const TextStyle(
                  color: AppColors.blocked, fontWeight: FontWeight.w700)),
        ),
      if (startedByLabel(ix.startedBy).isNotEmpty)
        Padding(
          padding: const EdgeInsets.only(top: 4),
          child: Text(startedByLabel(ix.startedBy),
              style: const TextStyle(color: AppColors.dim)),
        ),
    ];
    if ((ix.message ?? '').isNotEmpty) {
      detail.add(const SizedBox(height: 8));
      detail.add(appMarkdown(ix.message!));
    }
    // Reuse the transcript's per-tool renderers (Bash → "$ cmd", Edit → diff, …)
    // so the prompt shows the same formatted detail as the TUI instead of raw
    // JSON. A synthetic tool item is all toolDetailBody needs.
    if ((ix.toolInput ?? '').isNotEmpty) {
      detail.add(const SizedBox(height: 8));
      detail.add(toolDetailBody(
          Entry(
            id: '',
            kind: EntryKind.tool,
            toolName: ix.toolName,
            toolInput: ix.toolInput,
          ),
          agent: widget.session.agent));
    }
    if ((ix.plan ?? '').isNotEmpty) {
      detail.add(const SizedBox(height: 8));
      detail.add(appMarkdown(ix.plan!));
    }

    final reject = ix.options.firstWhere(
      (o) => o.reject,
      orElse: () =>
          const DecisionOption(label: 'Reject', value: 'deny', reject: true),
    );

    final options = ix.options.isNotEmpty
        ? ix.options
        : const [
            DecisionOption(label: 'Allow', value: 'allow'),
            DecisionOption(label: 'Deny', value: 'deny', reject: true),
          ];

    Widget optionButton(DecisionOption o, bool primary) {
      void onTap() {
        if (o.reject) {
          setState(() => _denying = true);
        } else {
          _respond(optionRespond(
              sessionId: _sid,
              kind: kind,
              value: o.value,
              requestId: ix.requestId));
        }
      }

      final label = Text(o.label);
      return Padding(
        padding: const EdgeInsets.only(bottom: 8),
        child: primary
            ? FilledButton(onPressed: _busy ? null : onTap, child: label)
            : OutlinedButton(onPressed: _busy ? null : onTap, child: label),
      );
    }

    return [
      _scrollBody(detail),
      const SizedBox(height: 16),
      if (!_denying)
        for (var i = 0; i < options.length; i++)
          optionButton(options[i], i == 0)
      else ...[
        TextField(
          controller: _text,
          autofocus: true,
          minLines: 1,
          maxLines: 4,
          decoration: InputDecoration(
            hintText: reject.placeholder.isNotEmpty
                ? reject.placeholder
                : 'Reason (optional)',
            border: const OutlineInputBorder(),
          ),
        ),
        const SizedBox(height: 12),
        FilledButton(
          onPressed: _busy
              ? null
              : () => _respond(
                  optionRespond(
                    sessionId: _sid,
                    kind: kind,
                    value: reject.value,
                    reason: _text.text,
                    requestId: ix.requestId,
                  ),
                ),
          child: const Text('Send'),
        ),
      ],
      if (_busy)
        const Padding(
          padding: EdgeInsets.only(top: 12),
          child: LinearProgressIndicator(),
        ),
    ];
  }

  /// A bot asks for a secret value. It is stored on the node and
  /// the bot hears only its variable name; the field is hidden and cleared
  /// once sent, and the value is never kept anywhere else.
  List<Widget> _secretRequest(Interaction ix) {
    final name = ix.secretName ?? '';
    final options = ix.options.isNotEmpty
        ? ix.options
        : const [
            DecisionOption(label: 'Save', value: 'save'),
            DecisionOption(label: 'Decline', value: 'decline', reject: true),
          ];
    return [
      _scrollBody([
        Text(
          name.isEmpty ? 'Secret requested' : 'Secret requested · $name',
          style: const TextStyle(
            color: AppColors.secondary,
            fontWeight: FontWeight.w700,
          ),
        ),
        if ((ix.message ?? '').isNotEmpty) ...[
          const SizedBox(height: 8),
          appMarkdown(ix.message!),
        ],
        const SizedBox(height: 8),
        Text(
          'Kept on this node; the bot sees only \$$name.',
          style: const TextStyle(color: AppColors.dim),
        ),
      ]),
      const SizedBox(height: 12),
      TextField(
        key: const ValueKey('secret-value'),
        controller: _secret,
        obscureText: true,
        enableSuggestions: false,
        autocorrect: false,
        enableIMEPersonalizedLearning: false,
        autofillHints: null,
        decoration: const InputDecoration(
          hintText: 'Value (hidden)',
          border: OutlineInputBorder(),
        ),
        onChanged: (_) => setState(() {}),
      ),
      const SizedBox(height: 12),
      for (var i = 0; i < options.length; i++)
        Padding(
          padding: const EdgeInsets.only(bottom: 8),
          child: options[i].reject
              ? OutlinedButton(
                  onPressed: _busy
                      ? null
                      : () => _respond(
                          optionRespond(
                            sessionId: _sid,
                            kind: 'secret',
                            value: options[i].value,
                            requestId: ix.requestId,
                          ),
                        ),
                  child: Text(options[i].label),
                )
              : FilledButton(
                  onPressed: _busy || _secret.text.isEmpty
                      ? null
                      : () => _respond(
                          secretRespond(
                            sessionId: _sid,
                            value: _secret.text,
                            requestId: ix.requestId,
                          ),
                        ),
                  child: Text(options[i].label),
                ),
        ),
      if (_busy)
        const Padding(
          padding: EdgeInsets.only(top: 12),
          child: LinearProgressIndicator(),
        ),
    ];
  }

  /// A bot asks the user to do a step it cannot: take over the
  /// session's terminal, then mark it done.
  List<Widget> _handoff(Interaction ix) {
    const takeover = 'takeover';
    final options = ix.options.isNotEmpty
        ? ix.options
        : const [
            DecisionOption(label: 'Take over', value: takeover),
            DecisionOption(label: 'Done', value: 'done'),
          ];
    void takeOver() {
      if (!widget.session.canOpenTerminal) {
        ScaffoldMessenger.of(context).showSnackBar(
          const SnackBar(content: Text('No terminal to take over')),
        );
        return;
      }
      setState(() => _tookOver = true);
      Navigator.of(context).push(
        MaterialPageRoute(
          builder: (_) => LiveScreenScreen(session: widget.session),
        ),
      );
    }

    return [
      _scrollBody([
        const Text(
          'Handoff',
          style: TextStyle(
            color: AppColors.secondary,
            fontWeight: FontWeight.w700,
          ),
        ),
        if ((ix.message ?? '').isNotEmpty) ...[
          const SizedBox(height: 8),
          appMarkdown(ix.message!),
        ],
        if ((ix.steps ?? '').isNotEmpty) ...[
          const SizedBox(height: 8),
          appMarkdown(ix.steps!),
        ],
      ]),
      const SizedBox(height: 16),
      for (var i = 0; i < options.length; i++)
        Padding(
          padding: const EdgeInsets.only(bottom: 8),
          child: _handoffButton(
            options[i],
            primary: _tookOver ? options[i].value == 'done' : i == 0,
            onTap: options[i].value == takeover
                ? takeOver
                : () => _respond(
                    optionRespond(
                      sessionId: _sid,
                      kind: 'handoff',
                      value: options[i].value,
                      requestId: ix.requestId,
                    ),
                  ),
          ),
        ),
      if (_busy)
        const Padding(
          padding: EdgeInsets.only(top: 12),
          child: LinearProgressIndicator(),
        ),
    ];
  }

  Widget _handoffButton(
    DecisionOption o, {
    required bool primary,
    required VoidCallback onTap,
  }) {
    final label = Text(o.label);
    final tap = _busy ? null : onTap;
    return primary
        ? FilledButton(onPressed: tap, child: label)
        : OutlinedButton(onPressed: tap, child: label);
  }

  /// Asks whether to submit with unanswered questions, as Codex does.
  Future<bool> _confirmUnanswered(int count) async {
    final noun = count == 1 ? 'question' : 'questions';
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Submit with unanswered questions?'),
        content: Text('$count unanswered $noun'),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Go back'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Proceed'),
          ),
        ],
      ),
    );
    return ok ?? false;
  }

  List<Widget> _questions(Interaction ix) {
    final qs = ix.questions;
    final drafts = _ensureDrafts(qs.length);
    final canSubmit = questionRespond(
          sessionId: _sid,
          questions: qs,
          drafts: drafts,
          allowUnanswered: ix.allowUnanswered,
        ) !=
        null;
    return [
      _scrollBody([
        for (var qi = 0; qi < qs.length; qi++)
          _questionCard(qs[qi], drafts[qi]),
      ]),
      const SizedBox(height: 12),
      FilledButton(
        onPressed: (_busy || !canSubmit)
            ? null
            : () async {
                final p = questionRespond(
                  sessionId: _sid,
                  questions: qs,
                  drafts: drafts,
                  allowUnanswered: ix.allowUnanswered,
                  requestId: ix.requestId,
                );
                if (p == null) return;
                final unanswered = unansweredCount(qs, drafts);
                if (ix.allowUnanswered &&
                    unanswered > 0 &&
                    !await _confirmUnanswered(unanswered)) {
                  return;
                }
                if (mounted) _respond(p);
              },
        child: const Text('Submit'),
      ),
      const SizedBox(height: 8),
      if (ix.cancelInterrupts)
        OutlinedButton(
          onPressed:
              _busy
                  ? null
                  : () => _respond(interruptRespond(
                      sessionId: _sid, requestId: ix.requestId)),
          child: const Text('Interrupt'),
        )
      else
        OutlinedButton(
          onPressed: _busy
              ? null
              : () => _respond(
                  clarifyRespond(
                      sessionId: _sid,
                      questions: qs,
                      drafts: drafts,
                      requestId: ix.requestId),
                ),
          child: const Text('Chat about this'),
        ),
      if (_busy)
        const Padding(
          padding: EdgeInsets.only(top: 12),
          child: LinearProgressIndicator(),
        ),
    ];
  }

  Widget _questionCard(QuestionSpec q, QuestionDraft d) {
    final oi = otherIndex(q);
    final rows = <Widget>[];
    if ((q.header ?? '').isNotEmpty) {
      rows.add(
        Align(
          alignment: Alignment.centerLeft,
          child: Chip(label: Text(q.header!)),
        ),
      );
    }
    if ((q.question ?? '').isNotEmpty) {
      rows.add(
        Padding(
          padding: const EdgeInsets.symmetric(vertical: 8),
          child: Text(q.question!),
        ),
      );
    }
    final labels = q.allowNotes ? [...q.options] : [...q.options, otherLabel];
    // Per-option metadata is valid only for real options; the synthetic
    // otherLabel row carries neither a description nor a preview.
    String? desc(int i) {
      if (i >= q.optionDescriptions.length) return null;
      final s = q.optionDescriptions[i];
      return s.isEmpty ? null : s;
    }

    String? preview(int i) {
      if (i >= q.optionPreviews.length) return null;
      final s = q.optionPreviews[i];
      return s.isEmpty ? null : s;
    }

    if (q.multiSelect) {
      for (var i = 0; i < labels.length; i++) {
        rows.add(
          CheckboxListTile(
            contentPadding: EdgeInsets.zero,
            controlAffinity: ListTileControlAffinity.leading,
            title: Text(labels[i]),
            subtitle: desc(i) != null ? Text(desc(i)!) : null,
            value: d.toggles.contains(i),
            onChanged: (_) => setState(() {
              d.toggles.contains(i) ? d.toggles.remove(i) : d.toggles.add(i);
            }),
          ),
        );
      }
    } else {
      rows.add(
        RadioGroup<int>(
          groupValue: d.chosen,
          onChanged: (v) => setState(() => d.chosen = v ?? -1),
          child: Column(
            children: [
              for (var i = 0; i < labels.length; i++) ...[
                RadioListTile<int>(
                  contentPadding: EdgeInsets.zero,
                  title: Text(labels[i]),
                  subtitle: desc(i) != null ? Text(desc(i)!) : null,
                  value: i,
                ),
                if (preview(i) != null)
                  Padding(
                    padding: const EdgeInsets.only(left: 16, bottom: 8),
                    child: codeBlock(preview(i)!),
                  ),
              ],
            ],
          ),
        ),
      );
    }
    // oi is -1 when the question takes notes: there is no custom row to show.
    final showCustom =
        oi >= 0 && (q.multiSelect ? d.toggles.contains(oi) : d.chosen == oi);
    if (showCustom) {
      rows.add(
        TextField(
          autofocus: true,
          decoration: const InputDecoration(
            labelText: 'Your answer',
            border: OutlineInputBorder(),
          ),
          onChanged: (v) => setState(() => d.custom = v),
        ),
      );
    }
    if (q.allowNotes) {
      if (d.noteOpen) {
        rows.add(
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: TextField(
              autofocus: true,
              decoration: InputDecoration(
                labelText: 'Note',
                border: const OutlineInputBorder(),
                suffixIcon: IconButton(
                  tooltip: 'Remove note',
                  icon: const Icon(Icons.close),
                  onPressed: () => setState(() {
                    d.note = '';
                    d.noteOpen = false;
                  }),
                ),
              ),
              onChanged: (v) => setState(() => d.note = v),
            ),
          ),
        );
      } else {
        rows.add(
          Align(
            alignment: Alignment.centerLeft,
            child: TextButton.icon(
              icon: const Icon(Icons.edit_note),
              label: const Text('Add note'),
              onPressed: () => setState(() => d.noteOpen = true),
            ),
          ),
        );
      }
    }
    return Card(
      child: Padding(
        padding: const EdgeInsets.all(12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: rows,
        ),
      ),
    );
  }
}

/// The prompt's scrolling part; the fixed parts around it set the panel's
/// minimum height.
Widget _scrollBody(List<Widget> children) => Flexible(
      child: _Scrolling(
        child: SingleChildScrollView(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: children,
          ),
        ),
      ),
    );

/// A scroll area fits any height, so its minimum intrinsic height is zero and
/// the panel's is just its fixed parts.
class _Scrolling extends SingleChildRenderObjectWidget {
  const _Scrolling({required Widget super.child});

  @override
  RenderObject createRenderObject(BuildContext context) => _RenderScrolling();
}

class _RenderScrolling extends RenderProxyBox {
  @override
  double computeMinIntrinsicHeight(double width) => 0;
}
