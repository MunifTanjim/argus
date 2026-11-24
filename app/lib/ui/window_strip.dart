import 'package:flutter/foundation.dart';
import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import 'theme.dart';

const double kMacTitleStripHeight = 28;
const Color kWindowStripColor = AppColors.canvas;
const MethodChannel windowChannel = MethodChannel('argus/window');

/// On macOS the title bar is transparent and the content extends under it, so
/// a strip above the navigator holds the traffic lights and drags the window.
class WindowStrip extends StatelessWidget {
  const WindowStrip({super.key, required this.child});

  final Widget child;

  @override
  Widget build(BuildContext context) {
    if (defaultTargetPlatform != TargetPlatform.macOS) return child;
    return Column(
      children: [
        GestureDetector(
          key: const ValueKey('window-strip'),
          behavior: HitTestBehavior.opaque,
          onPanStart: (_) => windowChannel.invokeMethod<void>('startDrag'),
          onDoubleTap: () => windowChannel.invokeMethod<void>('zoom'),
          child: const SizedBox(
            height: kMacTitleStripHeight,
            width: double.infinity,
            child: ColoredBox(color: kWindowStripColor),
          ),
        ),
        Expanded(child: child),
      ],
    );
  }
}
