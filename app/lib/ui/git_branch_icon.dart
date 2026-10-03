import 'package:flutter/material.dart';

// Material Icons has no git branch, so this draws the Nerd Font glyph as text.
// A const IconData on the terminal font would make release icon tree-shaking
// subset that font down to this one glyph.
class GitBranchIcon extends StatelessWidget {
  const GitBranchIcon({super.key, this.size, this.color});

  final double? size;
  final Color? color;

  @override
  Widget build(BuildContext context) {
    final theme = IconTheme.of(context);
    final size = this.size ?? theme.size ?? 24;
    return SizedBox.square(
      dimension: size,
      child: Center(
        child: Text(
          '\uF418',
          style: TextStyle(
            fontFamily: 'JetBrainsMonoNerdFontMono',
            fontSize: size,
            height: 1,
            color: color ?? theme.color,
          ),
        ),
      ),
    );
  }
}
