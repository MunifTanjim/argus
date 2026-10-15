import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

import '../core/result.dart';
import '../data/session_repository.dart';
import '../models/enums.dart';
import '../models/session.dart';
import '../state/prompt_drafts.dart';
import '../state/respond_view_model.dart';
import 'interaction_panel.dart';
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

/// The prompt input, or the pending prompt in its place, between a status line
/// and a model line.
class SessionComposer extends ConsumerStatefulWidget {
  const SessionComposer({super.key, required this.session, this.inputNotice});

  final Session session;

  /// Shown in place of the input when argus cannot send to the session.
  final String? inputNotice;

  @override
  ConsumerState<SessionComposer> createState() => _SessionComposerState();
}

class _SessionComposerState extends ConsumerState<SessionComposer> {
  late final RespondViewModel _vm;
  final _text = TextEditingController();
  Timer? _tick;
  bool _collapsed = false;

  String get _sid => widget.session.id;

  @override
  void initState() {
    super.initState();
    _vm = RespondViewModel(ref.read(sessionRepositoryProvider));
    _vm.sendInput.addListener(_refresh);
    final draft = ref.read(promptDraftsProvider.notifier).get(_sid);
    _text.text = draft;
    _text.selection = TextSelection.collapsed(offset: draft.length);
    _syncTick();
  }

  @override
  void didUpdateWidget(SessionComposer old) {
    super.didUpdateWidget(old);
    if (_promptKey(old.session.interaction) !=
        _promptKey(widget.session.interaction)) {
      _collapsed = false;
    }
    if (old.session.id != _sid) {
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
    super.dispose();
  }

  bool get _canSend =>
      widget.session.promptReady &&
      !_vm.running &&
      _text.text.trim().isNotEmpty;

  Future<void> _send() async {
    if (!_canSend) return;
    final t = _text.text.trim();
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

  @override
  Widget build(BuildContext context) {
    final s = widget.session;
    final notice = widget.inputNotice;
    final pending = _pending(s.interaction);
    final mq = MediaQuery.of(context);
    final showPanel = pending && notice == null;
    final Widget body;
    if (notice != null) {
      body = _notice(notice);
    } else if (pending) {
      body = _collapsed
          ? const SizedBox.shrink()
          : Flexible(
              child: ConstrainedBox(
                constraints: BoxConstraints(
                    maxHeight: (mq.size.height - mq.viewInsets.bottom) * 0.6),
                child: InteractionPanel(
                    key: ValueKey(_promptKey(s.interaction)), session: s),
              ),
            );
    } else {
      body = _input(s);
    }
    return Material(
      color: AppColors.canvas,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        crossAxisAlignment: CrossAxisAlignment.stretch,
        children: [
          _rule(
            label: composerStatus(s, DateTime.now()),
            trailing: showPanel
                ? IconButton(
                    tooltip: _collapsed ? 'Expand' : 'Collapse',
                    visualDensity: VisualDensity.compact,
                    icon: Icon(
                        _collapsed ? Icons.expand_less : Icons.expand_more),
                    onPressed: () => setState(() => _collapsed = !_collapsed),
                  )
                : null,
          ),
          body,
          _rule(label: modelLine(s.summary)),
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
              child: VoiceInputField(
                controller: _text,
                onChanged: _onChanged,
                field: (suffixIcon) => TextField(
                  controller: _text,
                  minLines: 1,
                  maxLines: 6,
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
            // While sending is blocked, a tap on the disabled button says why.
            Tooltip(
              message: s.promptReady ? 'Send' : 'Wait for idle to send',
              triggerMode: s.promptReady
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

  // A divider with a label set into its right end, like the TUI's rules.
  Widget _rule({String? label, Widget? trailing}) {
    const line = Divider(height: 1, color: AppColors.border);
    // The label sits on the line and truncates when it alone exceeds the width.
    Widget onLine(String t) => Stack(
          alignment: Alignment.center,
          children: [
            line,
            Align(
              alignment: Alignment.centerRight,
              child: ColoredBox(
                color: AppColors.canvas,
                child: Padding(
                  padding: const EdgeInsets.symmetric(horizontal: 6),
                  child: Text(t,
                      maxLines: 1,
                      softWrap: false,
                      overflow: TextOverflow.ellipsis,
                      style: mono.copyWith(color: AppColors.dim)),
                ),
              ),
            ),
          ],
        );
    return SizedBox(
      height: trailing == null ? 20 : 36,
      child: Row(children: [
        const SizedBox(width: 12, child: line),
        Expanded(child: label != null ? onLine(label) : line),
        ?trailing,
        const SizedBox(width: 12, child: line),
      ]),
    );
  }
}
