import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../models/agent.dart';
import '../models/session.dart';
import '../state/question_cursor.dart';
import '../state/respond_params.dart';
import 'code_block.dart';
import 'theme.dart';

/// Below this panel width the option preview stacks under the options.
const double _sidePreviewMinWidth = 600;

/// The options of a permission or plan decision: the server's, or Allow/Deny
/// when it sent none.
List<DecisionOption> decisionOptions(Interaction ix) => ix.options.isNotEmpty
    ? ix.options
    : const [
        DecisionOption(label: 'Allow', value: 'allow'),
        DecisionOption(label: 'Deny', value: 'deny', reject: true),
      ];

/// The option that takes a free-text reason.
DecisionOption decisionReject(Interaction ix) => ix.options.firstWhere(
  (o) => o.reject,
  orElse: () =>
      const DecisionOption(label: 'Reject', value: 'deny', reject: true),
);

/// An agent's questions answered with the keyboard, as in the TUI: one question
/// at a time, a highlight separate from the committed answer, and a Submit tab
/// when there are several questions.
class DesktopQuestions extends StatefulWidget {
  const DesktopQuestions({
    super.key,
    required this.session,
    required this.interaction,
    required this.busy,
    required this.onRespond,
  });

  final Session session;
  final Interaction interaction;
  final bool busy;
  final void Function(Map<String, dynamic> params) onRespond;

  @override
  State<DesktopQuestions> createState() => _DesktopQuestionsState();
}

class _DesktopQuestionsState extends State<DesktopQuestions> {
  final _focus = FocusNode(debugLabel: 'DesktopQuestions');
  late QuestionCursor _c;
  List<TextEditingController> _custom = [];
  List<TextEditingController> _notes = [];
  List<FocusNode> _customFocus = [];
  List<FocusNode> _noteFocus = [];

  /// Esc closed the custom field on the highlighted other row; moving the
  /// highlight or changing tabs opens it again.
  bool _customClosed = false;

  Interaction get _ix => widget.interaction;
  List<QuestionSpec> get _qs => _ix.questions;

  @override
  void initState() {
    super.initState();
    _reset();
  }

  @override
  void didUpdateWidget(DesktopQuestions old) {
    super.didUpdateWidget(old);
    if (old.interaction.requestId != _ix.requestId ||
        old.interaction.questions.length != _qs.length) {
      // The old fields stay mounted until this frame is built.
      final custom = _custom, notes = _notes;
      final customFocus = _customFocus, noteFocus = _noteFocus;
      WidgetsBinding.instance.addPostFrameCallback(
        (_) => _disposeFields(custom, notes, customFocus, noteFocus),
      );
      _reset();
    }
  }

  void _reset() {
    _c = QuestionCursor(_qs, allowUnanswered: _ix.allowUnanswered);
    _customClosed = false;
    final c = _c;
    _custom = [
      for (var i = 0; i < _qs.length; i++)
        _draftField((t) => c.drafts[i].custom = t),
    ];
    _notes = [
      for (var i = 0; i < _qs.length; i++)
        _draftField((t) => c.drafts[i].note = t),
    ];
    _customFocus = [for (final _ in _qs) FocusNode()];
    _noteFocus = [for (final _ in _qs) FocusNode()];
  }

  /// A controller that stores its text through [store] and rebuilds, so the
  /// tab label's ✓ follows typing.
  TextEditingController _draftField(void Function(String text) store) {
    final controller = TextEditingController();
    var last = '';
    controller.addListener(() {
      if (controller.text == last) return;
      last = controller.text;
      store(last);
      if (mounted) setState(() {});
    });
    return controller;
  }

  void _disposeFields(
    List<TextEditingController> custom,
    List<TextEditingController> notes,
    List<FocusNode> customFocus,
    List<FocusNode> noteFocus,
  ) {
    for (final c in [...custom, ...notes]) {
      c.dispose();
    }
    for (final f in [...customFocus, ...noteFocus]) {
      f.dispose();
    }
  }

  @override
  void dispose() {
    _disposeFields(_custom, _notes, _customFocus, _noteFocus);
    _focus.dispose();
    super.dispose();
  }

  bool get _onQuestion => !_c.confirming && !_c.onSubmitTab;

  bool get _customActive =>
      _onQuestion && _c.otherActive(_c.tab) && !_customClosed;

  bool get _noteActive => _onQuestion && _c.drafts[_c.tab].noteOpen;

  FocusNode? get _activeField => _noteActive
      ? _noteFocus[_c.tab]
      : (_customActive ? _customFocus[_c.tab] : null);

  /// Applies [change], then moves focus to the open field (or back to the panel)
  /// once the frame that shows it is built.
  void _update(VoidCallback change) {
    // A field that closes takes its focus with it, so remember whether the
    // panel had focus before the change.
    final hadFocus = _focus.hasFocus;
    setState(change);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !hadFocus) return;
      final target = _activeField ?? _focus;
      if (!target.hasPrimaryFocus) target.requestFocus();
    });
  }

  void _move(VoidCallback m) => _update(() {
    m();
    _customClosed = false;
  });

  void _act(CursorOutcome o) {
    final sid = widget.session.id;
    switch (o) {
      case CursorOutcome.none:
        return;
      case CursorOutcome.submit:
        final p = questionRespond(
          sessionId: sid,
          questions: _qs,
          drafts: _c.drafts,
          allowUnanswered: _ix.allowUnanswered,
          requestId: _ix.requestId,
        );
        if (p != null) widget.onRespond(p);
      case CursorOutcome.cancel:
        widget.onRespond(
          interruptRespond(sessionId: sid, requestId: _ix.requestId),
        );
      case CursorOutcome.chat:
        widget.onRespond(
          _ix.cancelInterrupts
              ? interruptRespond(sessionId: sid, requestId: _ix.requestId)
              : clarifyRespond(
                  sessionId: sid,
                  questions: _qs,
                  drafts: _c.drafts,
                  requestId: _ix.requestId,
                ),
        );
    }
  }

  KeyEventResult _onKey(FocusNode _, KeyEvent e) {
    if (e is KeyUpEvent || widget.busy || _qs.isEmpty) {
      return KeyEventResult.ignored;
    }
    final kb = HardwareKeyboard.instance;
    if (kb.isMetaPressed || kb.isControlPressed || kb.isAltPressed) {
      return KeyEventResult.ignored;
    }
    final k = e.logicalKey;
    final plain = !kb.isShiftPressed;
    final field = _customActive || _noteActive;

    if (k == LogicalKeyboardKey.escape) {
      if (!field) return KeyEventResult.ignored;
      _update(() {
        if (_noteActive) {
          _c.drafts[_c.tab].noteOpen = false;
        } else {
          _customClosed = true;
        }
      });
      return KeyEventResult.handled;
    }
    if (k == LogicalKeyboardKey.arrowUp) {
      _move(_c.up);
      return KeyEventResult.handled;
    }
    if (k == LogicalKeyboardKey.arrowDown) {
      _move(_c.down);
      return KeyEventResult.handled;
    }
    if (k == LogicalKeyboardKey.enter || k == LogicalKeyboardKey.numpadEnter) {
      late CursorOutcome o;
      _move(() {
        if (_noteActive) _c.drafts[_c.tab].noteOpen = false;
        o = _c.enter();
      });
      _act(o);
      return KeyEventResult.handled;
    }
    if (k == LogicalKeyboardKey.space &&
        !field &&
        _onQuestion &&
        _qs[_c.tab].multiSelect) {
      _update(_c.toggle);
      return KeyEventResult.handled;
    }
    if (field) return KeyEventResult.ignored; // the field takes typed keys

    if (k == LogicalKeyboardKey.arrowLeft) {
      _move(_c.prevTab);
      return KeyEventResult.handled;
    }
    if (k == LogicalKeyboardKey.arrowRight) {
      _move(_c.nextTab);
      return KeyEventResult.handled;
    }
    if (plain && k == LogicalKeyboardKey.keyK) {
      _move(_c.up);
      return KeyEventResult.handled;
    }
    if (plain && k == LogicalKeyboardKey.keyJ) {
      _move(_c.down);
      return KeyEventResult.handled;
    }
    if (plain &&
        k == LogicalKeyboardKey.keyN &&
        _onQuestion &&
        _qs[_c.tab].allowNotes) {
      _update(() => _c.drafts[_c.tab].noteOpen = true);
      return KeyEventResult.handled;
    }
    if (plain && k == LogicalKeyboardKey.keyC && !_c.confirming) {
      _act(CursorOutcome.chat);
      return KeyEventResult.handled;
    }
    return KeyEventResult.ignored;
  }

  void _tapOption(int q, int i) {
    if (widget.busy) return;
    _focus.requestFocus();
    late CursorOutcome o;
    _move(() => o = _c.tapOption(q, i));
    _act(o);
  }

  void _tapControl(int i) {
    if (widget.busy) return;
    _focus.requestFocus();
    late CursorOutcome o;
    _move(() {
      _c.submitSel = i;
      o = _c.enter();
    });
    _act(o);
  }

  void _tapTab(int i) {
    if (widget.busy || _c.confirming) return;
    _focus.requestFocus();
    _move(() => _c.tab = i);
  }

  @override
  Widget build(BuildContext context) {
    if (_qs.isEmpty) return const SizedBox.shrink();
    return Focus(
      focusNode: _focus,
      autofocus: true,
      onKeyEvent: _onKey,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (_c.isMulti) ...[_tabBar(), const SizedBox(height: 12)],
          if (_c.confirming)
            ..._confirmBody()
          else if (_c.onSubmitTab)
            ..._submitTabBody()
          else
            ..._questionBody(),
          if (!_c.confirming) ...[
            const SizedBox(height: 12),
            Text(
              _ix.cancelInterrupts ? 'c · interrupt' : 'c · chat about this',
              style: const TextStyle(color: AppColors.dim, fontSize: 12),
            ),
          ],
        ],
      ),
    );
  }

  String _tabLabel(int i) {
    final h = _qs[i].header ?? '';
    final label = h.isEmpty ? 'Q${i + 1}' : h;
    return _c.answered(i) ? '✓ $label' : label;
  }

  Widget _tabBar() {
    const style = TextStyle(fontSize: 13, fontWeight: FontWeight.w600);
    const pad = EdgeInsets.symmetric(horizontal: 8, vertical: 3);
    const gap = 6.0;
    final labels = [
      for (var i = 0; i < _qs.length; i++) _tabLabel(i),
      'Submit',
    ];
    return LayoutBuilder(
      builder: (context, box) {
        final scaler = MediaQuery.textScalerOf(context);
        var need = gap * (labels.length - 1);
        for (final l in labels) {
          final tp = TextPainter(
            text: TextSpan(text: l, style: style),
            textDirection: TextDirection.ltr,
            textScaler: scaler,
          )..layout();
          need += tp.width + pad.horizontal;
          tp.dispose();
        }
        if (need > box.maxWidth) {
          final pos = _c.tab + 1 > _qs.length ? _qs.length : _c.tab + 1;
          return Text(
            'Question $pos/${_qs.length}',
            style: const TextStyle(color: AppColors.dim),
          );
        }
        return Wrap(
          spacing: gap,
          children: [
            for (var i = 0; i < labels.length; i++)
              GestureDetector(
                onTap: () => _tapTab(i),
                child: Container(
                  padding: pad,
                  decoration: BoxDecoration(
                    color: i == _c.tab ? AppColors.accent : AppColors.border,
                    borderRadius: BorderRadius.circular(4),
                  ),
                  child: Text(
                    labels[i],
                    style: style.copyWith(
                      color: i == _c.tab
                          ? AppColors.canvas
                          : AppColors.secondary,
                    ),
                  ),
                ),
              ),
          ],
        );
      },
    );
  }

  List<Widget> _questionBody() {
    final tab = _c.tab;
    final q = _qs[tab];
    final d = _c.drafts[tab];
    final header = q.header ?? '';
    final agent = agentLabel(widget.session.agent);
    return [
      if (!_c.isMulti) ...[
        Wrap(
          spacing: 8,
          crossAxisAlignment: WrapCrossAlignment.center,
          children: [
            Text(
              '${agent.isEmpty ? 'Claude' : agent} is asking',
              style: const TextStyle(
                color: AppColors.accent,
                fontWeight: FontWeight.w700,
              ),
            ),
            if (header.isNotEmpty) Chip(label: Text(header)),
          ],
        ),
        const SizedBox(height: 8),
      ],
      if ((q.question ?? '').isNotEmpty) ...[
        appMarkdown(q.question!),
        const SizedBox(height: 8),
      ],
      _optionsWithPreview(tab),
      if (q.allowNotes)
        if (d.noteOpen)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: TextField(
              controller: _notes[tab],
              focusNode: _noteFocus[tab],
              decoration: const InputDecoration(
                labelText: 'Note',
                isDense: true,
                border: OutlineInputBorder(),
              ),
            ),
          )
        else if (d.note.trim().isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(top: 8),
            child: Text(
              'note: ${d.note.trim()}',
              style: const TextStyle(color: AppColors.dim),
            ),
          ),
    ];
  }

  Widget _optionsWithPreview(int tab) {
    final q = _qs[tab];
    final options = _options(tab);
    final hl = _c.highlight[tab];
    final preview = !q.multiSelect && hl < q.optionPreviews.length
        ? q.optionPreviews[hl]
        : '';
    if (preview.isEmpty) return options;
    final box = Container(
      key: const ValueKey('question-preview'),
      padding: const EdgeInsets.all(8),
      decoration: BoxDecoration(
        border: Border.all(color: AppColors.border),
        borderRadius: BorderRadius.circular(6),
      ),
      child: codeBlock(preview),
    );
    return LayoutBuilder(
      builder: (context, c) => c.maxWidth < _sidePreviewMinWidth
          ? Column(
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [options, const SizedBox(height: 8), box],
            )
          : Row(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Expanded(flex: 2, child: options),
                const SizedBox(width: 12),
                Expanded(flex: 3, child: box),
              ],
            ),
    );
  }

  Widget _options(int tab) {
    final q = _qs[tab];
    final d = _c.drafts[tab];
    final labels = _c.labels(tab);
    final oi = otherIndex(q);
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        for (var i = 0; i < labels.length; i++)
          _optionRow(
            highlighted: i == _c.highlight[tab],
            marked: q.multiSelect ? d.toggles.contains(i) : d.chosen == i,
            multi: q.multiSelect,
            onTap: () => _tapOption(tab, i),
            label: i == oi
                ? (_customActive
                      ? TextField(
                          controller: _custom[tab],
                          focusNode: _customFocus[tab],
                          decoration: const InputDecoration(
                            hintText: 'Type your own answer',
                            isDense: true,
                            border: InputBorder.none,
                          ),
                        )
                      : Text(d.custom.isEmpty ? otherLabel : '✎ ${d.custom}'))
                : Text(labels[i]),
            description: i < q.optionDescriptions.length
                ? q.optionDescriptions[i].trim()
                : '',
          ),
      ],
    );
  }

  Widget _optionRow({
    required bool highlighted,
    required bool marked,
    required bool multi,
    required VoidCallback onTap,
    required Widget label,
    required String description,
  }) {
    final icon = multi
        ? (marked ? Icons.check_box : Icons.check_box_outline_blank)
        : (marked ? Icons.radio_button_checked : Icons.radio_button_unchecked);
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(4),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
        decoration: BoxDecoration(
          color: highlighted ? AppColors.border : null,
          borderRadius: BorderRadius.circular(4),
        ),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Icon(
              icon,
              size: 18,
              color: marked ? AppColors.accent : AppColors.dim,
            ),
            const SizedBox(width: 8),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  DefaultTextStyle.merge(
                    style: TextStyle(
                      color: highlighted ? AppColors.text : AppColors.secondary,
                      fontWeight: highlighted ? FontWeight.w700 : null,
                    ),
                    child: label,
                  ),
                  if (description.isNotEmpty)
                    Text(
                      description,
                      style: const TextStyle(
                        color: AppColors.dim,
                        fontSize: 12,
                      ),
                    ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }

  List<Widget> _submitTabBody() {
    String summary(int i) {
      final a = questionAnswer(_qs[i], _c.drafts[i]);
      return switch (a) {
        final List<String> l => l.join(', '),
        final String s => s,
        _ => '',
      };
    }

    return [
      const Text(
        'Review answers',
        style: TextStyle(color: AppColors.accent, fontWeight: FontWeight.w700),
      ),
      const SizedBox(height: 8),
      for (var i = 0; i < _qs.length; i++) ...[
        Text.rich(
          TextSpan(
            children: [
              TextSpan(
                text: (_qs[i].header ?? '').isEmpty
                    ? 'Q${i + 1}'
                    : _qs[i].header!,
                style: const TextStyle(fontWeight: FontWeight.w700),
              ),
              const TextSpan(text: ': '),
              if (summary(i).isEmpty)
                const TextSpan(
                  text: '(not answered)',
                  style: TextStyle(color: AppColors.dim),
                )
              else
                TextSpan(text: summary(i)),
            ],
          ),
        ),
        if (_qs[i].allowNotes && _c.drafts[i].note.trim().isNotEmpty)
          Padding(
            padding: const EdgeInsets.only(left: 12),
            child: Text(
              'note: ${_c.drafts[i].note.trim()}',
              style: const TextStyle(color: AppColors.dim),
            ),
          ),
      ],
      const SizedBox(height: 12),
      ..._controls(['Submit', _ix.cancelInterrupts ? 'Interrupt' : 'Cancel']),
    ];
  }

  List<Widget> _confirmBody() {
    final n = unansweredCount(_qs, _c.drafts);
    return [
      const Text(
        'Submit with unanswered questions?',
        style: TextStyle(color: AppColors.accent, fontWeight: FontWeight.w700),
      ),
      Text(
        '$n unanswered ${n == 1 ? 'question' : 'questions'}',
        style: const TextStyle(color: AppColors.dim),
      ),
      const SizedBox(height: 12),
      ..._controls(['Proceed', 'Go back']),
    ];
  }

  List<Widget> _controls(List<String> labels) => [
    for (var i = 0; i < labels.length; i++)
      InkWell(
        onTap: () => _tapControl(i),
        borderRadius: BorderRadius.circular(4),
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 6),
          decoration: BoxDecoration(
            color: i == _c.submitSel ? AppColors.border : null,
            borderRadius: BorderRadius.circular(4),
          ),
          child: Text(
            labels[i],
            style: TextStyle(
              color: i == _c.submitSel ? AppColors.text : AppColors.secondary,
              fontWeight: i == _c.submitSel ? FontWeight.w700 : null,
            ),
          ),
        ),
      ),
  ];
}

/// A permission or plan decision picked with the keyboard: the options form a
/// highlighted list, and the reject option opens a reason field.
class DesktopDecision extends StatefulWidget {
  const DesktopDecision({
    super.key,
    required this.session,
    required this.interaction,
    required this.kind,
    required this.detail,
    required this.busy,
    required this.onRespond,
  });

  final Session session;
  final Interaction interaction;

  /// `permission` or `plan`.
  final String kind;

  /// The heading, message, tool detail and plan, built by [InteractionPanel].
  final List<Widget> detail;
  final bool busy;
  final void Function(Map<String, dynamic> params) onRespond;

  @override
  State<DesktopDecision> createState() => _DesktopDecisionState();
}

class _DesktopDecisionState extends State<DesktopDecision> {
  final _focus = FocusNode(debugLabel: 'DesktopDecision');
  final _reasonFocus = FocusNode();
  final _reason = TextEditingController();
  int _highlight = 0;
  bool _reasonOpen = false;

  Interaction get _ix => widget.interaction;
  List<DecisionOption> get _options => decisionOptions(_ix);

  @override
  void didUpdateWidget(DesktopDecision old) {
    super.didUpdateWidget(old);
    if (old.interaction.requestId != _ix.requestId || old.kind != widget.kind) {
      _highlight = 0;
      _reasonOpen = false;
      _reason.clear();
    }
  }

  @override
  void dispose() {
    _focus.dispose();
    _reasonFocus.dispose();
    _reason.dispose();
    super.dispose();
  }

  /// Applies [change], then moves focus to the reason field (or back to the
  /// panel) once the frame that shows it is built.
  void _update(VoidCallback change) {
    final hadFocus = _focus.hasFocus;
    setState(change);
    WidgetsBinding.instance.addPostFrameCallback((_) {
      if (!mounted || !hadFocus) return;
      final target = _reasonOpen ? _reasonFocus : _focus;
      if (!target.hasPrimaryFocus) target.requestFocus();
    });
  }

  void _select(int i) {
    final o = _options[i];
    if (o.reject) {
      _update(() {
        _highlight = i;
        _reasonOpen = true;
      });
      return;
    }
    widget.onRespond(
      optionRespond(
        sessionId: widget.session.id,
        kind: widget.kind,
        value: o.value,
        requestId: _ix.requestId,
      ),
    );
  }

  void _sendReject() => widget.onRespond(
    optionRespond(
      sessionId: widget.session.id,
      kind: widget.kind,
      value: decisionReject(_ix).value,
      reason: _reason.text,
      requestId: _ix.requestId,
    ),
  );

  void _move(int d) => _update(
    () => _highlight = (_highlight + d).clamp(0, _options.length - 1),
  );

  KeyEventResult _onKey(FocusNode _, KeyEvent e) {
    if (e is KeyUpEvent || widget.busy) return KeyEventResult.ignored;
    final kb = HardwareKeyboard.instance;
    if (kb.isMetaPressed || kb.isControlPressed || kb.isAltPressed) {
      return KeyEventResult.ignored;
    }
    final k = e.logicalKey;
    final plain = !kb.isShiftPressed;

    if (k == LogicalKeyboardKey.escape) {
      if (!_reasonOpen) return KeyEventResult.ignored;
      _update(() => _reasonOpen = false);
      return KeyEventResult.handled;
    }
    if (k == LogicalKeyboardKey.arrowUp) {
      _move(-1);
      return KeyEventResult.handled;
    }
    if (k == LogicalKeyboardKey.arrowDown) {
      _move(1);
      return KeyEventResult.handled;
    }
    if (k == LogicalKeyboardKey.enter || k == LogicalKeyboardKey.numpadEnter) {
      if (_reasonOpen) {
        _sendReject();
      } else {
        _select(_highlight);
      }
      return KeyEventResult.handled;
    }
    // The reason field takes typed keys.
    if (_reasonOpen) return KeyEventResult.ignored;
    if (plain && k == LogicalKeyboardKey.keyK) {
      _move(-1);
      return KeyEventResult.handled;
    }
    if (plain && k == LogicalKeyboardKey.keyJ) {
      _move(1);
      return KeyEventResult.handled;
    }
    return KeyEventResult.ignored;
  }

  void _tap(int i) {
    if (widget.busy) return;
    _focus.requestFocus();
    setState(() => _highlight = i);
    _select(i);
  }

  @override
  Widget build(BuildContext context) {
    final options = _options;
    final reject = decisionReject(_ix);
    return Focus(
      focusNode: _focus,
      autofocus: true,
      onKeyEvent: _onKey,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          ...widget.detail,
          const SizedBox(height: 16),
          if (_reasonOpen)
            TextField(
              controller: _reason,
              focusNode: _reasonFocus,
              autofocus: true,
              decoration: InputDecoration(
                hintText: reject.placeholder.isNotEmpty
                    ? reject.placeholder
                    : 'Reason (optional)',
                border: const OutlineInputBorder(),
              ),
            )
          else
            for (var i = 0; i < options.length; i++)
              InkWell(
                onTap: () => _tap(i),
                borderRadius: BorderRadius.circular(4),
                child: Container(
                  padding: const EdgeInsets.symmetric(
                    horizontal: 8,
                    vertical: 6,
                  ),
                  decoration: BoxDecoration(
                    color: i == _highlight ? AppColors.border : null,
                    borderRadius: BorderRadius.circular(4),
                  ),
                  child: Text(
                    options[i].label,
                    style: TextStyle(
                      color: i == _highlight
                          ? AppColors.text
                          : AppColors.secondary,
                      fontWeight: i == _highlight ? FontWeight.w700 : null,
                    ),
                  ),
                ),
              ),
          if (widget.busy)
            const Padding(
              padding: EdgeInsets.only(top: 12),
              child: LinearProgressIndicator(),
            ),
        ],
      ),
    );
  }
}
