import 'dart:async';
import 'dart:math' as math;

import 'package:flutter/foundation.dart' show listEquals;
import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../models/entry.dart';
import '../models/enums.dart';
import '../models/session.dart';
import '../state/grouping.dart';
import '../state/prompt_drafts.dart';
import '../state/respond_view_model.dart';
import 'interaction_panel.dart';
import 'prompt_completion.dart';
import 'text_measure.dart';
import 'theme.dart';
import 'usage_format.dart';
import 'voice_input_field.dart';

String interactionLabel(Interaction ix) => switch (ix.kind) {
  InteractionKind.permission =>
    (ix.toolName ?? '').isNotEmpty
        ? 'Permission: ${ix.toolName}'
        : 'Permission',
  InteractionKind.plan => 'Plan review',
  InteractionKind.question => 'Question',
  InteractionKind.idle => 'Reply',
  InteractionKind.unknown => 'Respond',
};

String respondElsewhereLabel(FrontendKind f) =>
    f == FrontendKind.vscode ? 'Respond in VSCode' : 'Respond in your terminal';

/// "12s", "3m 04s", "1h 02m".
String formatElapsed(Duration d) {
  final s = d.inSeconds < 0 ? 0 : d.inSeconds;
  String two(int n) => n.toString().padLeft(2, '0');
  if (s < 60) return '${s}s';
  if (s < 3600) return '${s ~/ 60}m ${two(s % 60)}s';
  return '${s ~/ 3600}h ${two(s ~/ 60 % 60)}m';
}

/// A new prompt gets fresh panel state and opens expanded.
Object _promptKey(Interaction? ix) => ix == null
    ? 0
    : (
        ix.kind,
        ix.requestId,
        ix.toolName,
        ix.toolInput,
        ix.plan,
        ix.questions.map((q) => q.question).join('\u0000'),
      );

/// The status line's text: the pending prompt, or what the session is doing;
/// null while it waits for a prompt.
String? composerStatus(Session s, DateTime now) {
  if (s.interactionPending) return interactionLabel(s.interaction!);
  switch (s.status) {
    case SessionStatus.working:
      final since = s.statusSince;
      return since == null
          ? 'Working…'
          : 'Working… ${formatElapsed(now.difference(since))}';
    case SessionStatus.starting:
      return 'Starting…';
    default:
      return null;
  }
}

/// The model chip's parts: model, effort, usage, cost, each left out when
/// unknown.
List<(String, Color)> modelParts(Summary? s, ArgusTokens t) {
  if (s == null) return const [];
  final parts = <(String, Color)>[
    if ((s.modelName ?? '').isNotEmpty)
      (s.modelName!, hexColor(s.modelColor) ?? t.textDim),
    if ((s.effort ?? '').isNotEmpty) (s.effort!, t.textDim),
  ];
  if (s.hasContext) {
    final pct = fmtPct(s.contextPct);
    parts.add((
      s.tokens > 0 ? '${fmtTokens(s.tokens)} ($pct)' : pct,
      contextColor(s.contextPct, t),
    ));
  } else if (s.tokens > 0) {
    parts.add((fmtTokens(s.tokens), t.textDim));
  }
  if (s.hasCost) {
    parts.add(('\$${s.costUsd.toStringAsFixed(2)}', t.textDim));
  }
  return parts;
}

/// The first [n] of [parts].
TextSpan modelLine(List<(String, Color)> parts, int n, ArgusTokens t) =>
    metaSpan(parts.take(n), t);

/// The space around the composer's box, and around the resume bar that takes
/// its place in a history transcript.
const dockPadding = EdgeInsets.fromLTRB(10, 6, 10, 10);

/// One IconButton slot: the toolbar's height and the width of Send and the
/// mic.
const _slot = 48.0;
const _toolbarEdge = 6.0;
const _chipPad = 8.0;
const _chipPadV = 2.0;
const _maxQueued = 3;

final _whitespace = RegExp(r'\s+');

// The completion menu's least distance from the top of the screen.
const _menuMargin = 8.0;

/// The completion menu while the cursor ends a word that starts with
/// [trigger]. [dismissed] hides it until the cursor leaves that word.
class _Menu {
  _Menu(this.trigger, this.query);
  final String trigger;
  String query;
  bool dismissed = false;
  int selected = 0;
  List<PromptMatch> matches = const [];
}

/// The composer box: a status strip, then the queue, the input, and the
/// toolbar, or the pending prompt or a notice in their place.
class SessionComposer extends ConsumerStatefulWidget {
  const SessionComposer({
    super.key,
    required this.session,
    this.inputNotice,
    this.queue = const [],
    this.onSent,
  });

  final Session session;

  final VoidCallback? onSent;

  final List<QueuedPrompt> queue;

  /// Shown in place of the input when argus cannot send to the session.
  final String? inputNotice;

  @override
  ConsumerState<SessionComposer> createState() => _SessionComposerState();
}

class _SessionComposerState extends ConsumerState<SessionComposer> {
  late final RespondViewModel _vm;
  final _text = TextEditingController();
  late final _focus = FocusNode(onKeyEvent: _menuKey);
  final _portal = OverlayPortalController()..show();
  Timer? _tick;

  /// Completion items by session id, then trigger.
  final _completions = <String, Map<String, List<PromptCompletion>>>{};
  _Menu? _menu;
  int _completeSeq = 0;
  bool _collapsed = false;

  /// The panel's height cap set by dragging the status line; null until then.
  double? _panelHeight;

  /// The model chip's height; measured once per change of text scale or
  /// default text style, not on every keystroke.
  double? _chipHeight;

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _chipHeight = null;
  }

  final _panelKey = GlobalKey();

  _RenderPanelBox? get _panelBox =>
      _panelKey.currentContext?.findRenderObject() as _RenderPanelBox?;

  /// The height of the panel's fixed parts, as last laid out.
  double get _panelMin => _panelBox?.minHeight ?? 0;

  bool _collapses(double height) => height <= _panelMin / 2;

  String get _sid => widget.session.id;

  @override
  void initState() {
    super.initState();
    _vm = RespondViewModel(ref.read(sessionRepositoryProvider));
    _vm.sendInput.addListener(_refresh);
    final draft = ref.read(promptDraftsProvider.notifier).get(_sid);
    _text.text = draft;
    _text.selection = TextSelection.collapsed(offset: draft.length);
    _text.addListener(_syncMenu);
    _focus.addListener(_refresh);
    _syncTick();
  }

  @override
  void didUpdateWidget(SessionComposer old) {
    super.didUpdateWidget(old);
    if (_promptKey(old.session.interaction) !=
        _promptKey(widget.session.interaction)) {
      _collapsed = false;
      _panelHeight = null;
    }
    if (old.session.id != _sid) {
      _completeSeq++;
      _menu = null;
      _text.text = ref.read(promptDraftsProvider.notifier).get(_sid);
    }
    _syncTick();
  }

  void _refresh() {
    if (mounted) setState(() {});
  }

  void _syncTick() {
    final working = widget.session.status == SessionStatus.working;
    if (working && _tick == null) {
      _tick = Timer.periodic(const Duration(seconds: 1), (_) => _refresh());
    } else if (!working) {
      _tick?.cancel();
      _tick = null;
    }
  }

  @override
  void dispose() {
    _tick?.cancel();
    _vm.sendInput.removeListener(_refresh);
    _vm.dispose();
    _text.dispose();
    _focus.dispose();
    super.dispose();
  }

  bool get _canSend =>
      widget.session.canPrompt && !_vm.running && _text.text.trim().isNotEmpty;

  Future<void> _send() async {
    if (!_canSend) return;
    final t = _text.text.trim();
    _completions.remove(_sid);
    _completeSeq++;
    _menu = null;
    await _vm.sendInput.execute((sessionId: _sid, text: t));
    if (!mounted) return;
    switch (_vm.sendInput.result) {
      case Ok():
        widget.onSent?.call();
        if (_text.text.trim() == t) {
          _text.clear();
          ref.read(promptDraftsProvider.notifier).clear(_sid);
        }
      case Error(:final error):
        ScaffoldMessenger.of(
          context,
        ).showSnackBar(SnackBar(content: Text('Failed: $error')));
      case null:
        break;
    }
  }

  void _onChanged(String v) {
    ref.read(promptDraftsProvider.notifier).set(_sid, v);
    setState(() {});
  }

  String? get _before {
    final at = _text.selection.baseOffset;
    return at < 0 || at > _text.text.length
        ? null
        : _text.text.substring(0, at);
  }

  List<PromptMatch> get _matches {
    final m = _menu;
    if (m == null || m.dismissed || !_focus.hasFocus) return const [];
    return m.matches;
  }

  List<PromptMatch> _matchFor(String trigger, String query) =>
      matchCompletions(_completions[_sid]?[trigger] ?? const [], query);

  // Entering a trigger word with no cached items asks the agent for them; a
  // later fetch supersedes it.
  void _syncMenu() {
    final before = _before;
    final q = before == null
        ? null
        : completionQuery(promptTriggers(widget.session.agent), before);
    final m = _menu;
    if (q == null) {
      if (m != null) setState(() => _menu = null);
      return;
    }
    if (m != null && m.trigger == q.trigger) {
      if (m.query != q.query) {
        setState(
          () => m
            ..query = q.query
            ..selected = 0
            ..matches = _matchFor(q.trigger, q.query),
        );
      }
      return;
    }
    setState(
      () =>
          _menu = _Menu(q.trigger, q.query)
            ..matches = _matchFor(q.trigger, q.query),
    );
    if (_completions[_sid]?.containsKey(q.trigger) ?? false) return;
    final seq = ++_completeSeq;
    ref.read(sessionRepositoryProvider).completePrompt(_sid, q.trigger).then((
      r,
    ) {
      if (!mounted || seq != _completeSeq) return;
      if (r case Ok(:final value) when value.isNotEmpty) {
        setState(() {
          (_completions[_sid] ??= {})[q.trigger] = value;
          final m = _menu;
          if (m != null && m.trigger == q.trigger) {
            m.matches = _matchFor(m.trigger, m.query);
          }
        });
      }
    });
  }

  // Keeps the text after the cursor and leaves the cursor after the name.
  void _select(PromptCompletion item) {
    final m = _menu;
    final before = _before;
    if (m == null || before == null) return;
    final after = _text.text.substring(before.length);
    final head = applyCompletion(before, m.trigger, m.query, item.name);
    _menu = null;
    _text.value = TextEditingValue(
      text: head + after,
      selection: TextSelection.collapsed(offset: head.length),
    );
    _onChanged(_text.text);
  }

  KeyEventResult _menuKey(FocusNode _, KeyEvent e) {
    final items = _matches;
    if (items.isEmpty || e is KeyUpEvent) return KeyEventResult.ignored;
    final m = _menu!;
    final n = items.length;
    final ctrl = HardwareKeyboard.instance.isControlPressed;
    final k = e.logicalKey;
    if (k == LogicalKeyboardKey.arrowDown ||
        ctrl && k == LogicalKeyboardKey.keyN) {
      setState(() => m.selected = (m.selected + 1) % n);
    } else if (k == LogicalKeyboardKey.arrowUp ||
        ctrl && k == LogicalKeyboardKey.keyP) {
      setState(() => m.selected = (m.selected - 1 + n) % n);
    } else if (k == LogicalKeyboardKey.enter ||
        k == LogicalKeyboardKey.numpadEnter ||
        k == LogicalKeyboardKey.tab) {
      _select(items[m.selected].item);
    } else if (k == LogicalKeyboardKey.escape) {
      setState(() => m.dismissed = true);
    } else {
      return KeyEventResult.ignored;
    }
    return KeyEventResult.handled;
  }

  // Dragging the status line resizes the panel: up to the full height, down
  // to its fixed parts, and past half of those it collapses. A drag past the
  // top or past short content reverses at once.
  void _dragStart(DragStartDetails _) {
    _panelHeight = _collapsed ? 0 : _panelBox?.size.height ?? 0;
  }

  void _dragUpdate(DragUpdateDetails d) {
    final box = _panelBox;
    // A new prompt mid-drag reset the height: go on from where it is.
    var h = _panelHeight ?? box?.size.height ?? 0;
    if (box != null) h = math.min(h, box.size.height);
    h = math.max(0.0, h - d.delta.dy);
    _panelHeight = h;
    final collapsed = _collapses(h);
    if (collapsed != _collapsed) {
      setState(() => _collapsed = collapsed);
    } else {
      // Only the cap changed: lay the panel out again without rebuilding it.
      box?.cap = h;
    }
  }

  void _toggle() => setState(() {
    _collapsed = !_collapsed;
    if (_collapses(_panelHeight ?? _panelMin)) _panelHeight = null;
  });

  @override
  Widget build(BuildContext context) =>
      LayoutBuilder(builder: (context, box) => _build(box.maxHeight));

  Widget _build(double maxHeight) {
    final t = context.tokens;
    final s = widget.session;
    final notice = widget.inputNotice;
    final pending = s.interactionPending;
    final showPanel = pending && notice == null;
    // A pending prompt waits on the user, so the box stands out; collapsed,
    // the status strip stands out more.
    final alert = showPanel && _collapsed;
    final blocked = showPanel && !_collapsed;
    final label = composerStatus(s, DateTime.now());
    Widget? strip;
    if (label != null) {
      strip = _strip(
        label,
        bg: alert
            ? t.blockedSurface
            : blocked
            ? t.canvas
            : t.surfaceRaised,
        color: showPanel ? t.blocked : t.textDim,
        trailing: showPanel
            ? IconButton(
                tooltip: _collapsed ? 'Expand' : 'Collapse',
                visualDensity: VisualDensity.compact,
                color: t.blocked,
                icon: Icon(_collapsed ? Icons.expand_less : Icons.expand_more),
                onPressed: _toggle,
              )
            : null,
      );
      if (showPanel) {
        strip = GestureDetector(
          behavior: HitTestBehavior.opaque,
          onTap: _collapsed ? _toggle : null,
          onVerticalDragStart: _dragStart,
          onVerticalDragUpdate: _dragUpdate,
          child: strip,
        );
      }
    }
    final Widget body;
    if (notice != null) {
      body = _notice(notice);
    } else if (pending) {
      // Hidden, not removed, when collapsed, so the answers so far are kept.
      body = Flexible(
        child: Visibility(
          visible: !_collapsed,
          maintainState: true,
          child: _PanelBox(
            key: _panelKey,
            cap: _panelHeight ?? maxHeight * 0.6,
            child: InteractionPanel(
              key: ValueKey(_promptKey(s.interaction)),
              session: s,
            ),
          ),
        ),
      );
    } else {
      // Empty, the box is one row: the input and the mic. Text brings the
      // toolbar with the model and Send; focus alone does not, so a tap on
      // the field does not make the box jump.
      final compact = _text.text.isEmpty;
      body = VoiceInputField(
        controller: _text,
        onChanged: _onChanged,
        // Over the placeholder in the input row, or in the toolbar left of
        // Send.
        micInset: compact
            ? const EdgeInsets.only(right: _toolbarEdge)
            : const EdgeInsets.only(right: _toolbarEdge + _slot),
        field: (micSlot) => AnimatedSize(
          duration: const Duration(milliseconds: 150),
          curve: Curves.easeOut,
          alignment: Alignment.topCenter,
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              if (widget.queue.isNotEmpty) _queueList(widget.queue),
              // Keyed, so the field keeps its state (focus, keyboard) as the
              // rows around it come and go.
              ConstrainedBox(
                key: const ValueKey('composer-input-row'),
                constraints: BoxConstraints(minHeight: compact ? _slot : 0),
                child: Row(
                  children: [
                    Expanded(child: _input(compact: compact)),
                    if (compact && micSlot != null)
                      Padding(
                        padding: const EdgeInsets.only(right: _toolbarEdge),
                        child: micSlot,
                      ),
                  ],
                ),
              ),
              if (!compact) _toolbar(s, micSlot),
            ],
          ),
        ),
      );
    }
    final radius = BorderRadius.circular(t.radius + 4);
    return Material(
      color: t.canvas,
      child: Padding(
        padding: dockPadding,
        child: OverlayPortal.overlayChildLayoutBuilder(
          controller: _portal,
          overlayChildBuilder: _menuOverlay,
          // In front, so the opaque status strip does not hide the top edge.
          // The Material behind gives the box its canvas color.
          child: DecoratedBox(
            key: const Key('composer-box'),
            position: DecorationPosition.foreground,
            decoration: BoxDecoration(
              borderRadius: radius,
              border: Border.all(
                color: alert
                    ? t.blockedBorder
                    : blocked
                    ? t.blocked
                    : t.line,
              ),
            ),
            child: ClipRRect(
              borderRadius: radius,
              child: Column(
                mainAxisSize: MainAxisSize.min,
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [?strip, body],
              ),
            ),
          ),
        ),
      ),
    );
  }

  /// The top of the box: what the session is doing, or the pending prompt.
  Widget _strip(
    String label, {
    required Color bg,
    required Color color,
    Widget? trailing,
  }) => Container(
    key: const Key('composer-status'),
    height: trailing == null ? 28 : 36,
    color: bg,
    padding: EdgeInsets.only(left: 12, right: trailing == null ? 12 : 0),
    child: Row(
      children: [
        Expanded(
          child: Text(
            label,
            maxLines: 1,
            softWrap: false,
            overflow: TextOverflow.ellipsis,
            style: mono.copyWith(color: color, fontSize: 11),
          ),
        ),
        ?trailing,
      ],
    ),
  );

  Widget _queueList(List<QueuedPrompt> q) {
    final style = Theme.of(
      context,
    ).textTheme.bodySmall?.copyWith(color: context.tokens.textDim);
    Widget line(String text) =>
        Text(text, maxLines: 1, overflow: TextOverflow.ellipsis, style: style);
    return Padding(
      padding: const EdgeInsets.fromLTRB(16, 4, 16, 0),
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          for (final p in q.take(_maxQueued))
            line(p.text.replaceAll(_whitespace, ' ').trim()),
          if (q.length > _maxQueued) line('+${q.length - _maxQueued} more'),
        ],
      ),
    );
  }

  // The same space above the text in both states. Empty, the text is
  // centered and one line fills the row's 48 dp; with text, the toolbar's
  // row starts right below it.
  Widget _input({required bool compact}) => TextField(
    controller: _text,
    focusNode: _focus,
    minLines: 1,
    maxLines: 6,
    textCapitalization: TextCapitalization.sentences,
    onChanged: _onChanged,
    decoration: InputDecoration(
      hintText: 'Reply',
      border: InputBorder.none,
      enabledBorder: InputBorder.none,
      focusedBorder: InputBorder.none,
      isDense: true,
      contentPadding: EdgeInsets.fromLTRB(12, 12, 12, compact ? 12 : 0),
    ),
  );

  static final _chipStyle = mono.copyWith(fontSize: 11);

  /// The model chip at the left; the mic slot and Send at the right. The
  /// chip is centered in the row, and as far from the box's left edge as
  /// from the row's top and bottom.
  Widget _toolbar(Session s, Widget? micSlot) {
    final chipHeight = _chipHeight ??=
        measureText(context, const TextSpan(text: ' '), _chipStyle).height +
        2 * _chipPadV +
        2;
    return Padding(
      padding: const EdgeInsets.only(right: _toolbarEdge),
      child: SizedBox(
        height: _slot,
        child: Row(
          children: [
            Expanded(
              child: Padding(
                padding: EdgeInsets.only(left: (_slot - chipHeight) / 2),
                child: Align(
                  key: const Key('model-chip-slot'),
                  alignment: Alignment.centerLeft,
                  child: _modelChip(s),
                ),
              ),
            ),
            ?micSlot,
            _sendButton(s),
          ],
        ),
      ),
    );
  }

  // The model chip's last fit: typing and the elapsed tick rebuild the
  // composer, and the chip need not be measured again.
  ({List<(String, Color)> parts, double width, TextScaler scaler, int n})?
      _chipFit;

  /// How many of [parts] fit in [maxWidth].
  int _chipFitCount(
      BuildContext context, List<(String, Color)> parts, double maxWidth) {
    final scaler = MediaQuery.textScalerOf(context);
    final fit = _chipFit;
    if (fit != null &&
        fit.width == maxWidth &&
        fit.scaler == scaler &&
        listEquals(fit.parts, parts)) {
      return fit.n;
    }
    final t = context.tokens;
    var n = parts.length;
    while (n > 0 &&
        measureText(context, modelLine(parts, n, t), _chipStyle).width +
                2 * _chipPad +
                2 >
            maxWidth) {
      n--;
    }
    _chipFit = (parts: parts, width: maxWidth, scaler: scaler, n: n);
    return n;
  }

  /// Model, effort, usage, and cost; as the TUI's bottom rule does, it drops
  /// cost, usage, effort, then the model, to fit without truncating.
  Widget _modelChip(Session s) {
    final t = context.tokens;
    final parts = modelParts(s.summary, t);
    // Measured and drawn with the same style, so a part drops only when it
    // does not fit.
    final style = _chipStyle;
    return LayoutBuilder(
      builder: (context, c) {
        final n = _chipFitCount(context, parts, c.maxWidth);
        if (n == 0) return const SizedBox.shrink();
        return Container(
          key: const Key('model-chip'),
          padding: const EdgeInsets.symmetric(
            horizontal: _chipPad,
            vertical: _chipPadV,
          ),
          decoration: BoxDecoration(
            border: Border.all(color: t.line),
            borderRadius: BorderRadius.circular(10),
          ),
          child: Text.rich(
            modelLine(parts, n, t),
            maxLines: 1,
            softWrap: false,
            style: style,
          ),
        );
      },
    );
  }

  // While sending is blocked, a tap on the disabled button says why.
  Widget _sendButton(Session s) => SizedBox(
    width: _slot,
    child: Center(
      child: Tooltip(
        message: s.canPrompt ? 'Send' : 'Wait for idle to send',
        triggerMode: s.canPrompt
            ? TooltipTriggerMode.longPress
            : TooltipTriggerMode.tap,
        child: IconButton.filled(
          iconSize: 18,
          padding: EdgeInsets.zero,
          constraints: const BoxConstraints.tightFor(width: 32, height: 32),
          icon: const Icon(Icons.arrow_upward),
          onPressed: _canSend ? _send : null,
        ),
      ),
    ),
  );

  // Over the transcript, above the composer box, so nothing resizes.
  Widget _menuOverlay(BuildContext context, OverlayChildLayoutInfo info) {
    final items = _matches;
    if (items.isEmpty) return const SizedBox.shrink();
    final top = MatrixUtils.transformPoint(
      info.childPaintTransform,
      Offset.zero,
    );
    final room = top.dy - 4 - _menuMargin;
    if (room <= 0) return const SizedBox.shrink();
    return Stack(
      children: [
        Positioned(
          left: top.dx,
          bottom: info.overlaySize.height - top.dy + 4,
          width: info.childSize.width,
          // Part of the field's tap region, so a click on a row keeps its focus.
          child: TextFieldTapRegion(
            child: PromptMenu(
              items: items,
              selected: _menu!.selected,
              maxHeight: room,
              onSelect: _select,
            ),
          ),
        ),
      ],
    );
  }

  Widget _notice(String text) => Padding(
    padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
    child: Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      mainAxisSize: MainAxisSize.min,
      children: [
        Text(
          text,
          style: TextStyle(
            color: context.tokens.textSecondary,
            fontWeight: FontWeight.bold,
          ),
        ),
        const SizedBox(height: 2),
        Text(
          "argus can't send input to this session",
          style: TextStyle(color: context.tokens.textSecondary, fontSize: 12),
        ),
      ],
    ),
  );
}

/// Lays the panel out at most [cap] tall but never shorter than its minimum
/// intrinsic height, so its fixed parts always fit.
class _PanelBox extends SingleChildRenderObjectWidget {
  const _PanelBox({super.key, required this.cap, required Widget super.child});

  final double cap;

  @override
  _RenderPanelBox createRenderObject(BuildContext context) =>
      _RenderPanelBox(cap);

  @override
  void updateRenderObject(BuildContext context, _RenderPanelBox r) =>
      r.cap = cap;
}

class _RenderPanelBox extends RenderProxyBox {
  _RenderPanelBox(this._cap);

  double _cap;
  set cap(double v) {
    if (v == _cap) return;
    _cap = v;
    markNeedsLayout();
  }

  double minHeight = 0;

  BoxConstraints _capped(BoxConstraints c, double min) =>
      c.copyWith(maxHeight: c.constrainHeight(math.max(_cap, min)));

  @override
  Size computeDryLayout(BoxConstraints constraints) => constraints.constrain(
    child!.getDryLayout(
      _capped(constraints, child!.getMinIntrinsicHeight(constraints.maxWidth)),
    ),
  );

  @override
  void performLayout() {
    minHeight = child!.getMinIntrinsicHeight(constraints.maxWidth);
    child!.layout(_capped(constraints, minHeight), parentUsesSize: true);
    size = constraints.constrain(child!.size);
  }
}
