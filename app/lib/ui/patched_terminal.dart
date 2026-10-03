import 'package:xterm/xterm.dart';

/// xterm 4.0.0 erases left of the cursor (`CSI 1 K`) wrongly: at column 0 it
/// reads cell -1 and throws, and it never erases the cursor cell itself.
class PatchedTerminal extends Terminal {
  PatchedTerminal({super.maxLines});

  @override
  void eraseLineLeft() {
    final line = buffer.currentLine;
    line.isWrapped = false;
    line.eraseRange(0, buffer.cursorX + 1, cursor);
  }
}
