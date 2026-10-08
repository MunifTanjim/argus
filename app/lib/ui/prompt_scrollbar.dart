import 'dart:async';
import 'dart:math' as math;
import 'dart:ui' show lerpDouble;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart' show ScrollDirection;
import 'package:flutter/services.dart';
import 'package:super_sliver_list/super_sliver_list.dart';

import '../models/entry.dart';
import 'theme.dart';
import 'transcript_feed.dart';

/// Snap reach (logical px along the track) around a prompt tick.
const double kPromptSnapDistance = 12;

const double _touchWidth = 24;
const double _trackWidth = 4;
const double _thumbHeight = 28;
const double _dotSize = 6;
const Duration _fadeDelay = Duration(milliseconds: 1500);
const Duration _fadeDuration = Duration(milliseconds: 200);
const Duration _glideDuration = Duration(milliseconds: 120);
const Duration _bendDuration = Duration(milliseconds: 150);

/// Half-height (logical px) of the fisheye lens around the finger.
const double kFisheyeRadius = 96;

/// Fisheye strength: spacing right at the finger grows by (1 + k).
const double kFisheyeStrength = 3;

/// How far (logical px) the track bends out at the finger, and how quickly the
/// bend falls off above and below it.
const double kBulgeAmplitude = 24;
const double kBulgeSigma = 36;

/// Points within [kFisheyeRadius] spread away from [focus], continuously and
/// in order; the focus itself and points beyond the lens stay put.
double fisheyeY(double y, double focus) {
  final d = y - focus;
  final n = d.abs() / kFisheyeRadius;
  if (n >= 1) return y;
  final g = (kFisheyeStrength + 1) * n / (kFisheyeStrength * n + 1);
  return focus + d.sign * g * kFisheyeRadius;
}

/// Leftward bend (logical px) of the track at [y] while the finger is at [focus].
double bulgeOffset(double y, double focus) {
  final z = (y - focus) / kBulgeSigma;
  return kBulgeAmplitude * math.exp(-z * z / 2);
}

bool _isPrompt(FeedRow r) =>
    r is EntryFeedRow && r.entry.kind == EntryKind.user;

double _indexFraction(int index, int rowCount) =>
    rowCount <= 1 ? 0 : index / (rowCount - 1);

List<int> promptRowIndexes(List<FeedRow> rows) => [
  for (var i = 0; i < rows.length; i++)
    if (_isPrompt(rows[i])) i,
];

List<double> promptTickFractions(List<FeedRow> rows) => [
  for (final i in promptRowIndexes(rows)) _indexFraction(i, rows.length),
];

int fractionToIndex(double fraction, int rowCount) {
  if (rowCount <= 1) return 0;
  return (fraction.clamp(0.0, 1.0) * (rowCount - 1)).round();
}

/// Returns null when no tick is within [distance] px of the pointer.
int? snapIndex(
  double dragFraction,
  double trackHeight,
  List<double> tickFractions,
  List<int> tickIndexes, {
  double distance = kPromptSnapDistance,
}) {
  int? best;
  var bestDist = double.infinity;
  for (var i = 0; i < tickFractions.length; i++) {
    final d = (dragFraction - tickFractions[i]).abs() * trackHeight;
    if (d <= distance && d < bestDist) {
      bestDist = d;
      best = tickIndexes[i];
    }
  }
  return best;
}

/// An index-based scrollbar. Ticks mark user prompts; a drag near a tick snaps
/// the list to that prompt.
class PromptScrollbar extends StatefulWidget {
  const PromptScrollbar({
    super.key,
    required this.rows,
    required this.listController,
    required this.scrollController,
    required this.child,
  });

  final List<FeedRow> rows;
  final ListController listController;
  final ScrollController scrollController;
  final Widget child;

  @override
  State<PromptScrollbar> createState() => _PromptScrollbarState();
}

class _PromptScrollbarState extends State<PromptScrollbar> {
  bool _shown = false;
  bool _touching = false;
  Timer? _hideTimer;
  int? _snapped;
  // Where the thumb sits while touched: the finger, or the snapped tick.
  double? _dragFraction;
  double? _focusY; // the finger on the track while touched
  double _lastFocus = 0; // keeps the bend centered while it eases out
  bool _refreshScheduled = false;
  late List<int> _indexes;
  late List<double> _fractions;

  void _indexPrompts() {
    _indexes = promptRowIndexes(widget.rows);
    _fractions = promptTickFractions(widget.rows);
  }

  @override
  void initState() {
    super.initState();
    _indexPrompts();
    widget.listController.addListener(_scheduleRefresh);
    widget.scrollController.addListener(_scheduleRefresh);
  }

  @override
  void didUpdateWidget(PromptScrollbar old) {
    super.didUpdateWidget(old);
    if (!identical(old.rows, widget.rows)) _indexPrompts();
    if (old.listController != widget.listController) {
      old.listController.removeListener(_scheduleRefresh);
      widget.listController.addListener(_scheduleRefresh);
    }
    if (old.scrollController != widget.scrollController) {
      old.scrollController.removeListener(_scheduleRefresh);
      widget.scrollController.addListener(_scheduleRefresh);
    }
  }

  @override
  void dispose() {
    _hideTimer?.cancel();
    widget.listController.removeListener(_scheduleRefresh);
    widget.scrollController.removeListener(_scheduleRefresh);
    super.dispose();
  }

  // The list controller notifies during layout, so rebuild after the frame.
  void _scheduleRefresh() {
    if (_refreshScheduled) return;
    _refreshScheduled = true;
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _refreshScheduled = false;
      if (mounted) setState(() {});
    });
  }

  void _show() {
    _hideTimer?.cancel();
    if (!_shown) setState(() => _shown = true);
  }

  void _hideLater() {
    _hideTimer?.cancel();
    _hideTimer = Timer(_fadeDelay, () {
      if (mounted && !_touching) setState(() => _shown = false);
    });
  }

  bool _onUserScroll(UserScrollNotification n) {
    if (n.direction == ScrollDirection.idle) {
      if (!_touching) _hideLater();
    } else {
      _show();
    }
    return false;
  }

  bool get _canScroll {
    final sc = widget.scrollController;
    return sc.hasClients &&
        sc.position.hasContentDimensions &&
        sc.position.maxScrollExtent > 0 &&
        widget.listController.isAttached;
  }

  double _thumbFraction() {
    if (_touching && _dragFraction != null) return _dragFraction!;
    final p = widget.scrollController.position;
    if (p.pixels >= p.maxScrollExtent - 1) return 1;
    final first = widget.listController.visibleRange?.$1 ?? 0;
    return _indexFraction(first, widget.rows.length);
  }

  void _jumpTo(int index) {
    final sc = widget.scrollController;
    widget.listController.jumpToItem(
      index: index,
      scrollController: sc,
      alignment: 0,
    );
    // Aligning a trailing row to the top asks for an offset past the end;
    // pin to the end so the feed resumes following instead of overscrolling.
    final p = sc.position;
    if (p.pixels > p.maxScrollExtent) sc.jumpTo(p.maxScrollExtent);
  }

  void _drag(double y, double trackHeight) {
    final f = (y / trackHeight).clamp(0.0, 1.0);
    _focusY = _lastFocus = f * trackHeight;
    // Snap against where the dots show under the fisheye, with the reach
    // scaled by the lens magnification so it does not shrink as ticks spread.
    final shown = [
      for (final t in _fractions)
        fisheyeY(t * trackHeight, _focusY!) / trackHeight,
    ];
    final snap = snapIndex(
      f,
      trackHeight,
      shown,
      _indexes,
      distance: kPromptSnapDistance * (1 + kFisheyeStrength),
    );
    if (snap != null) {
      if (_snapped != snap) HapticFeedback.selectionClick();
      _snapped = snap;
      _dragFraction = _indexFraction(snap, widget.rows.length);
      _jumpTo(snap);
    } else {
      _snapped = null;
      _dragFraction = f;
      _jumpTo(fractionToIndex(f, widget.rows.length));
    }
    setState(() {});
  }

  void _release() {
    _touching = false;
    _snapped = null;
    _dragFraction = null;
    _focusY = null;
    setState(() {});
    _hideLater();
  }

  /// [bend] runs from 0 (straight) to 1 (fully bent).
  Widget _bar(double h, double bend) {
    final focus = _focusY ?? _lastFocus;
    double shownY(double y) =>
        bend == 0 ? y : lerpDouble(y, fisheyeY(y, focus), bend)!;
    double out(double y) => bend * bulgeOffset(y, focus);
    final thumbY = shownY(_thumbFraction() * h);
    return Stack(
      clipBehavior: Clip.none,
      children: [
        Positioned.fill(
          child: CustomPaint(
            painter: _TrackPainter(
              centerX: _touchWidth / 2,
              bend: bend,
              focus: focus,
              color: AppColors.dim.withValues(alpha: 0.15),
            ),
          ),
        ),
        for (var i = 0; i < _indexes.length; i++)
          _tick(
            widget.rows[_indexes[i]] as EntryFeedRow,
            shownY(_fractions[i] * h),
            h,
            out,
          ),
        AnimatedPositioned(
          key: const ValueKey('prompt-scrollbar-thumb'),
          // Under the finger it tracks exactly; otherwise it glides between
          // rows instead of stepping.
          duration: _touching ? Duration.zero : _glideDuration,
          top: (thumbY - _thumbHeight / 2).clamp(0.0, h - _thumbHeight),
          right: (_touchWidth - _trackWidth) / 2 + out(thumbY),
          width: _trackWidth,
          height: _thumbHeight,
          child: DecoratedBox(
            decoration: BoxDecoration(
              color: AppColors.dim,
              borderRadius: BorderRadius.circular(_trackWidth / 2),
            ),
          ),
        ),
      ],
    );
  }

  Widget _tick(
    EntryFeedRow row,
    double y,
    double h,
    double Function(double) out,
  ) => Positioned(
    key: ValueKey('prompt-tick-${row.entry.id}'),
    top: (y - _dotSize / 2).clamp(0.0, h - _dotSize),
    right: (_touchWidth - _dotSize) / 2 + out(y),
    width: _dotSize,
    height: _dotSize,
    child: const DecoratedBox(
      decoration: BoxDecoration(
        color: AppColors.accent,
        shape: BoxShape.circle,
      ),
    ),
  );

  @override
  Widget build(BuildContext context) {
    final list = NotificationListener<UserScrollNotification>(
      onNotification: _onUserScroll,
      child: widget.child,
    );
    if (!_canScroll) return Stack(children: [list]);
    return Stack(
      children: [
        list,
        Positioned(
          top: 0,
          bottom: 0,
          right: 0,
          width: _touchWidth,
          child: LayoutBuilder(
            builder: (context, c) {
              final h = c.maxHeight;
              // Faded out, the strip lets touches through so the right edge
              // scrolls like the rest of the list.
              return IgnorePointer(
                ignoring: !_shown && !_touching,
                child: Listener(
                  key: const ValueKey('prompt-scrollbar-track'),
                  behavior: HitTestBehavior.opaque,
                  onPointerDown: (e) {
                    _touching = true;
                    _show();
                    _drag(e.localPosition.dy, h);
                  },
                  onPointerMove: (e) => _drag(e.localPosition.dy, h),
                  onPointerUp: (_) => _release(),
                  onPointerCancel: (_) => _release(),
                  child: AnimatedOpacity(
                    key: const ValueKey('prompt-scrollbar'),
                    opacity: _shown ? 1 : 0,
                    duration: _fadeDuration,
                    child: TweenAnimationBuilder<double>(
                      tween: Tween(end: _focusY != null ? 1 : 0),
                      duration: _bendDuration,
                      curve: Curves.easeOut,
                      builder: (context, bend, _) => _bar(h, bend),
                    ),
                  ),
                ),
              );
            },
          ),
        ),
      ],
    );
  }
}

class _TrackPainter extends CustomPainter {
  _TrackPainter({
    required this.centerX,
    required this.bend,
    required this.focus,
    required this.color,
  });

  final double centerX;
  final double bend;
  final double focus;
  final Color color;

  @override
  void paint(Canvas canvas, Size size) {
    double bendAt(double y) => bend * bulgeOffset(y, focus);
    final path = Path()..moveTo(centerX - bendAt(0), 0);
    for (var y = 2.0; y <= size.height; y += 2) {
      path.lineTo(centerX - bendAt(y), y);
    }
    canvas.drawPath(
      path,
      Paint()
        ..color = color
        ..style = PaintingStyle.stroke
        ..strokeWidth = _trackWidth
        ..strokeCap = StrokeCap.round,
    );
  }

  @override
  bool shouldRepaint(_TrackPainter old) =>
      old.centerX != centerX ||
      old.bend != bend ||
      old.focus != focus ||
      old.color != color;
}
