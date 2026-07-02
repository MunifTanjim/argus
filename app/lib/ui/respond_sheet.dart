import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/command.dart';
import '../core/result.dart';
import '../data/session_repository.dart';
import '../models/chunk.dart';
import '../models/enums.dart';
import '../models/session.dart';
import '../state/respond_params.dart';
import '../state/respond_view_model.dart';
import 'code_block.dart';
import 'theme.dart';
import 'tool_detail.dart';

/// Opens the respond sheet for [session]'s pending interaction.
Future<void> showRespondSheet(BuildContext context, Session session) {
  return showModalBottomSheet<void>(
    context: context,
    isScrollControlled: true,
    builder: (sheetCtx) => Padding(
      padding: EdgeInsets.only(
        bottom: MediaQuery.of(sheetCtx).viewInsets.bottom,
      ),
      child: RespondSheet(session: session),
    ),
  );
}

class RespondSheet extends ConsumerStatefulWidget {
  const RespondSheet({super.key, required this.session});
  final Session session;

  @override
  ConsumerState<RespondSheet> createState() => _RespondSheetState();
}

class _RespondSheetState extends ConsumerState<RespondSheet> {
  late final RespondViewModel _vm;
  bool _denying = false;
  final _text = TextEditingController();
  List<QuestionDraft>? _drafts;

  @override
  void initState() {
    super.initState();
    _vm = RespondViewModel(ref.read(sessionRepositoryProvider));
    _vm.respond.addListener(_onCommand);
    _vm.sendInput.addListener(_onCommand);
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
    _vm.sendInput.removeListener(_onCommand);
    _vm.dispose();
    _text.dispose();
    super.dispose();
  }

  String get _sid => widget.session.id;
  bool get _busy => _vm.running;

  Future<void> _respond(Map<String, dynamic> params) =>
      _finish(_vm.respond, () => _vm.respond.execute(params));

  Future<void> _sendInput(String text) => _finish(
    _vm.sendInput,
    () => _vm.sendInput.execute((sessionId: _sid, text: text)),
  );

  /// Runs [exec], then pops on success or shows the error from [cmd].
  Future<void> _finish(Command<void> cmd, Future<void> Function() exec) async {
    await exec();
    if (!mounted) return;
    switch (cmd.result) {
      case Ok():
        Navigator.of(context).pop();
      case Error(:final error):
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed: $error')));
      case null:
        break;
    }
  }

  @override
  Widget build(BuildContext context) {
    final ix = widget.session.interaction;
    if (ix == null) return const SizedBox.shrink();
    return SafeArea(
      // Align with heightFactor 1.0 caps width without forcing full height, so
      // the modal sheet shrink-wraps its content instead of covering the page.
      child: Align(
        alignment: Alignment.topCenter,
        heightFactor: 1.0,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxWidth: 520),
          child: Padding(
            padding: const EdgeInsets.all(16),
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: _body(ix),
            ),
          ),
        ),
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
        return _idle();
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
    final heading = kind == 'plan'
        ? 'Plan review'
        : (tool.isEmpty
            ? 'Permission requested'
            : 'Permission requested · $tool');

    final detail = <Widget>[
      Text(heading,
          style: const TextStyle(
              color: AppColors.secondary, fontWeight: FontWeight.w700)),
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
      detail.add(toolDetailBody(Item(
        id: '',
        kind: ItemKind.tool,
        toolName: ix.toolName,
        toolInput: ix.toolInput,
      )));
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
          _respond(optionRespond(sessionId: _sid, kind: kind, value: o.value));
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
      Flexible(
        child: SingleChildScrollView(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: detail,
          ),
        ),
      ),
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

  List<Widget> _idle() => [
    TextField(
      controller: _text,
      autofocus: true,
      minLines: 1,
      maxLines: 6,
      decoration: const InputDecoration(
        labelText: 'Reply',
        border: OutlineInputBorder(),
      ),
    ),
    const SizedBox(height: 12),
    FilledButton(
      onPressed: _busy
          ? null
          : () {
              final t = _text.text.trim();
              if (t.isEmpty) return;
              _sendInput(t);
            },
      child: const Text('Send'),
    ),
    if (_busy)
      const Padding(
        padding: EdgeInsets.only(top: 12),
        child: LinearProgressIndicator(),
      ),
  ];

  List<Widget> _questions(Interaction ix) {
    final qs = ix.questions;
    final drafts = _ensureDrafts(qs.length);
    final canSubmit =
        questionRespond(sessionId: _sid, questions: qs, drafts: drafts) != null;
    return [
      Flexible(
        child: SingleChildScrollView(
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              for (var qi = 0; qi < qs.length; qi++)
                _questionCard(qs[qi], drafts[qi]),
            ],
          ),
        ),
      ),
      const SizedBox(height: 12),
      FilledButton(
        onPressed: (_busy || !canSubmit)
            ? null
            : () {
                final p = questionRespond(
                  sessionId: _sid,
                  questions: qs,
                  drafts: drafts,
                );
                if (p != null) _respond(p);
              },
        child: const Text('Submit'),
      ),
      const SizedBox(height: 8),
      OutlinedButton(
        onPressed: _busy
            ? null
            : () => _respond(
                clarifyRespond(sessionId: _sid, questions: qs, drafts: drafts),
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
    final labels = [...q.options, otherLabel];
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
    final showCustom = q.multiSelect ? d.toggles.contains(oi) : d.chosen == oi;
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
