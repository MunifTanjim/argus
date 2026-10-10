import 'package:flutter/material.dart';

// Material Icons has no git glyphs, so these draw Nerd Font glyphs as text.
// A const IconData on the terminal font would make release icon tree-shaking
// subset that font down to these glyphs.
class NerdIcon extends StatelessWidget {
  const NerdIcon(this.glyph, {super.key, this.size, this.color});

  final String glyph;
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
          glyph,
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

class GitBranchIcon extends StatelessWidget {
  const GitBranchIcon({super.key, this.size, this.color});

  final double? size;
  final Color? color;

  @override
  Widget build(BuildContext context) =>
      NerdIcon('\uF418', size: size, color: color); // nf-oct-git_branch
}

class RepoIcon extends StatelessWidget {
  const RepoIcon({super.key, this.size, this.color});

  final double? size;
  final Color? color;

  @override
  Widget build(BuildContext context) =>
      NerdIcon('\uF401', size: size, color: color); // nf-oct-repo
}
