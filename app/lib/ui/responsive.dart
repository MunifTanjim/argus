import 'package:flutter/material.dart';

/// Default maximum content width. Keeps lists, forms, and transcripts readable
/// and centred on wide screens (tablets, foldables, resized/desktop windows)
/// instead of stretching edge to edge. On phones the available width is smaller
/// than this, so [CenteredBody] is a no-op there — no breakpoint needed.
const double kContentMaxWidth = 760;

/// Centres [child] horizontally and caps its width at [maxWidth]. Wrap a screen's
/// scrollable body or form in this so it reads as a column on large screens.
class CenteredBody extends StatelessWidget {
  const CenteredBody({
    super.key,
    required this.child,
    this.maxWidth = kContentMaxWidth,
  });

  final Widget child;
  final double maxWidth;

  @override
  Widget build(BuildContext context) {
    return Center(
      child: ConstrainedBox(
        constraints: BoxConstraints(maxWidth: maxWidth),
        child: child,
      ),
    );
  }
}
