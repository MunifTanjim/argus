import 'dart:async';
import 'dart:math' as math;

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
import 'running_shells_sheet.dart';
import 'running_subagents_sheet.dart';
import 'status_style.dart';
import 'theme.dart';
import 'voice_input_field.dart';

String interactionLabel(Interaction ix) => switch (ix.kind) {
      InteractionKind.permission => (ix.toolName ?? '').isNotEmpty
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

bool _pending(Interaction? ix) => ix != null && ix.kind != InteractionKind.idle;

/// The status line's text: the pending prompt, or what the session is doing;
/// null while it waits for a prompt.
String? composerStatus(Session s, DateTime now) {
  final ix = s.interaction;
  if (_pending(ix)) return interactionLabel(ix!);
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

/// The model line's text, `model · effort`, or null when neither is known.
String? modelLine(Summary? s) {
  final parts = [
    if ((s?.modelName ?? '').isNotEmpty) s!.modelName!,
    if ((s?.effort ?? '').isNotEmpty) s!.effort!,
  ];
  return parts.isEmpty ? null : parts.join(' · ');
}

/// The least tap target width a touch control gets.
const _countTapWidth = 48.0;
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

/// The prompt input, or the pending prompt in its place, between a status line
/// and a model line.
class SessionComposer extends ConsumerStatefulWidget {
  const SessionComposer(
      {super.key,
      required this.session,
      this.inputNotice,
      this.queue = const []});

  final Session session;

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
      widget.session.canPrompt &&
      !_vm.running &&
      _text.text.trim().isNotEmpty;

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
        if (_text.text.trim() == t) {
          _text.clear();
          ref.read(promptDraftsProvider.notifier).clear(_sid);
        }
      case Error(:final error):
        ScaffoldMessenger.of(context)
            .showSnackBar(SnackBar(content: Text('Failed: $error')));
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
        setState(() => m
          ..query = q.query
          ..selected = 0
          ..matches = _matchFor(q.trigger, q.query));
      }
      return;
    }
    setState(() => _menu = _Menu(q.trigger, q.query)
      ..matches = _matchFor(q.trigger, q.query));
    if (_completions[_sid]?.containsKey(q.trigger) ?? false) return;
    final seq = ++_completeSeq;
    ref
        .read(sessionRepositoryProvider)
        .completePrompt(_sid, q.trigger)
        .then((r) {
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
        selection: TextSelection.collapsed(offset: head.length));
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
    final s = widget.session;
    final notice = widget.inputNotice;
    final pending = _pending(s.interaction);
    final showPanel = pending && notice == null;
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
                key: ValueKey(_promptKey(s.interaction)), session: s),
          ),
        ),
      );
    } else {
      body = Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (widget.queue.isNotEmpty) _queueList(widget.queue),
          _input(s),
        ],
      );
    }
    // A collapsed prompt still waits on the user, so its line stands out.
    final alert = showPanel && _collapsed;
    final status = _rule(
      label: composerStatus(s, DateTime.now()),
      alert: alert,
      trailing: showPanel
          ? IconButton(
              tooltip: _collapsed ? 'Expand' : 'Collapse',
              visualDensity: VisualDensity.compact,
              color: alert ? statusColor(SessionStatus.awaitingInput) : null,
              icon: Icon(_collapsed ? Icons.expand_less : Icons.expand_more),
              onPressed: _toggle,
            )
          : null,
    );
    return Material(
      color: AppColors.canvas,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          if (showPanel)
            GestureDetector(
              behavior: HitTestBehavior.opaque,
              onTap: _collapsed ? _toggle : null,
              onVerticalDragStart: _dragStart,
              onVerticalDragUpdate: _dragUpdate,
              child: status,
            )
          else
            status,
          body,
          _rule(
            label: modelLine(s.summary),
            leading: s.subagents.isEmpty && s.shells.isEmpty
                ? null
                : Row(mainAxisSize: MainAxisSize.min, children: [
                    if (s.subagents.isNotEmpty) _subagentCount(s),
                    if (s.shells.isNotEmpty) _shellCount(s),
                  ]),
          ),
        ],
      ),
    );
  }

  Widget _queueList(List<QueuedPrompt> q) {
    final style = Theme.of(context)
        .textTheme
        .bodySmall
        ?.copyWith(color: AppColors.dim);
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

  Widget _input(Session s) => Padding(
        padding: const EdgeInsets.fromLTRB(12, 4, 4, 4),
        child: Row(
          crossAxisAlignment: CrossAxisAlignment.end,
          children: [
            Expanded(
              child: OverlayPortal.overlayChildLayoutBuilder(
                controller: _portal,
                overlayChildBuilder: _menuOverlay,
                child: VoiceInputField(
                  controller: _text,
                  onChanged: _onChanged,
                  field: (suffixIcon) => TextField(
                    controller: _text,
                    focusNode: _focus,
                    minLines: 1,
                    maxLines: 6,
                    textCapitalization: TextCapitalization.sentences,
                    onChanged: _onChanged,
                    decoration: InputDecoration(
                      hintText: 'Reply',
                      border: const OutlineInputBorder(),
                      isDense: true,
                      suffixIcon: suffixIcon,
                    ),
                  ),
                ),
              ),
            ),
            // While sending is blocked, a tap on the disabled button says why.
            Tooltip(
              message: s.canPrompt ? 'Send' : 'Wait for idle to send',
              triggerMode: s.canPrompt
                  ? TooltipTriggerMode.longPress
                  : TooltipTriggerMode.tap,
              child: IconButton(
                icon: const Icon(Icons.send),
                onPressed: _canSend ? _send : null,
              ),
            ),
          ],
        ),
      );

  // Over the transcript, right above the input, so nothing resizes.
  Widget _menuOverlay(BuildContext context, OverlayChildLayoutInfo info) {
    final items = _matches;
    if (items.isEmpty) return const SizedBox.shrink();
    final top =
        MatrixUtils.transformPoint(info.childPaintTransform, Offset.zero);
    final room = top.dy - 4 - _menuMargin;
    if (room <= 0) return const SizedBox.shrink();
    return Stack(children: [
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
    ]);
  }

  Widget _notice(String text) => Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 12),
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          mainAxisSize: MainAxisSize.min,
          children: [
            Text(text,
                style: const TextStyle(
                    color: AppColors.secondary, fontWeight: FontWeight.bold)),
            const SizedBox(height: 2),
            const Text("argus can't send input to this session",
                style: TextStyle(color: AppColors.secondary, fontSize: 12)),
          ],
        ),
      );

  Widget _subagentCount(Session s) => _count(
      Icons.smart_toy_outlined,
      s.subagents.length,
      () => showRunningSubagents(context, sessionId: s.id, agent: s.agent));

  Widget _shellCount(Session s) => _count(Icons.terminal, s.shells.length,
      () => showRunningShells(context, sessionId: s.id, agent: s.agent));

  // The tap target is only as tall as the model line, so the composer never
  // shifts when the count shows.
  Widget _count(IconData icon, int n, VoidCallback onTap) => InkWell(
        onTap: onTap,
        child: ConstrainedBox(
          constraints: const BoxConstraints(minWidth: _countTapWidth),
          child: Center(
            child: ColoredBox(
              color: AppColors.canvas,
              child: Padding(
                padding: const EdgeInsets.symmetric(horizontal: 6),
                child: Row(mainAxisSize: MainAxisSize.min, children: [
                  Icon(icon, size: 14, color: AppColors.dim),
                  const SizedBox(width: 4),
                  Text('$n',
                      style: mono.copyWith(color: AppColors.dim)),
                ]),
              ),
            ),
          ),
        ),
      );

  // A divider with a label set into its right end, like the TUI's rules.
  Widget _rule(
      {String? label, Widget? leading, Widget? trailing, bool alert = false}) {
    final bg = alert ? AppColors.awaitingSurface : AppColors.canvas;
    final line = Divider(
        height: 1, color: alert ? AppColors.awaitingBorder : AppColors.border);
    // The label sits on the line and truncates when it alone exceeds the width.
    Widget onLine(String t) => Stack(
          alignment: Alignment.center,
          children: [
            line,
            Align(
              alignment: Alignment.centerRight,
              child: ColoredBox(
                color: bg,
                child: Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 6),
                  child: Text(t,
                      maxLines: 1,
                      softWrap: false,
                      overflow: TextOverflow.ellipsis,
                      style: mono.copyWith(
                          color: alert
                              ? statusColor(SessionStatus.awaitingInput)
                              : AppColors.dim)),
                ),
              ),
            ),
          ],
        );
    return Container(
      height: trailing == null ? 20 : 36,
      color: bg,
      child: Row(children: [
        SizedBox(width: 12, child: line),
        ?leading,
        Expanded(child: label != null ? onLine(label) : line),
        ?trailing,
        SizedBox(width: 12, child: line),
      ]),
    );
  }
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
      child!.getDryLayout(_capped(
          constraints, child!.getMinIntrinsicHeight(constraints.maxWidth))));

  @override
  void performLayout() {
    minHeight = child!.getMinIntrinsicHeight(constraints.maxWidth);
    child!.layout(_capped(constraints, minHeight), parentUsesSize: true);
    size = constraints.constrain(child!.size);
  }
}
