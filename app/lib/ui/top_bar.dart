import 'package:flutter/material.dart';

import 'theme.dart';

/// The app's [AppBar]. With a leading icon, given or implied, the title starts
/// as far from the icon as the icon is from the edge. Material puts it twice
/// as far.
class TopBar extends StatelessWidget implements PreferredSizeWidget {
  const TopBar({
    super.key,
    this.leading,
    this.automaticallyImplyLeading = true,
    this.title,
    this.actions,
  });

  final Widget? leading;
  final bool automaticallyImplyLeading;
  final Widget? title;
  final List<Widget>? actions;

  @override
  Size get preferredSize => const Size.fromHeight(kToolbarHeight);

  @override
  Widget build(BuildContext context) {
    // The same test AppBar uses to imply a back or close button.
    final implied =
        automaticallyImplyLeading &&
        (Scaffold.maybeOf(context)?.hasDrawer == true ||
            ModalRoute.of(context)?.impliesAppBarDismissal == true);
    return AppBar(
      leading: leading,
      automaticallyImplyLeading: automaticallyImplyLeading,
      titleSpacing: leading != null || implied ? 0 : null,
      title: title,
      actions: actions,
    );
  }
}

/// An app bar title that opens details on tap: an optional [leading] mark,
/// the [text] on one line with an optional [subtitle] line under it, and a
/// trailing `▾` that says a tap opens them.
class TopBarTitle extends StatelessWidget {
  const TopBarTitle({
    super.key,
    required this.text,
    required this.onTap,
    this.leading,
    this.subtitle,
  });

  final String text;
  final VoidCallback onTap;
  final Widget? leading;

  /// A smaller second line in the link color, for example where the
  /// titled thing sits.
  final String? subtitle;

  @override
  Widget build(BuildContext context) {
    final t = context.tokens;
    return Align(
      alignment: Alignment.centerLeft,
      child: InkWell(
        borderRadius: BorderRadius.circular(t.radius),
        onTap: onTap,
        child: Padding(
          padding: const EdgeInsets.symmetric(vertical: 4),
          child: Row(
            mainAxisSize: MainAxisSize.min,
            children: [
              if (leading case final l?) ...[l, const SizedBox(width: 8)],
              Flexible(
                child: Column(
                  crossAxisAlignment: CrossAxisAlignment.start,
                  mainAxisSize: MainAxisSize.min,
                  children: [
                    // In the app bar's title style.
                    Text(text, maxLines: 1, overflow: TextOverflow.ellipsis),
                    if (subtitle case final s?)
                      Text(
                        s,
                        maxLines: 1,
                        overflow: TextOverflow.ellipsis,
                        style: TextStyle(color: t.link, fontSize: 12),
                      ),
                  ],
                ),
              ),
              const SizedBox(width: 2),
              Icon(Icons.expand_more, size: 18, color: t.textDim),
            ],
          ),
        ),
      ),
    );
  }
}
