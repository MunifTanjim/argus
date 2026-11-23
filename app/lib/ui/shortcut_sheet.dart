import 'package:flutter/material.dart';

import 'desktop_shortcuts.dart';

bool _sheetOpen = false;

Future<void> showShortcutSheet(BuildContext context) {
  if (_sheetOpen) return Future.value();
  _sheetOpen = true;
  return showDialog<void>(
    context: context,
    builder: (_) => const _ShortcutSheet(),
  );
}

class _ShortcutSheet extends StatefulWidget {
  const _ShortcutSheet();

  @override
  State<_ShortcutSheet> createState() => _ShortcutSheetState();
}

class _ShortcutSheetState extends State<_ShortcutSheet> {
  @override
  void dispose() {
    _sheetOpen = false;
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final rows = [
      ...shortcutDescriptions(),
      (keys: 'Enter', action: 'Send reply'),
      (keys: 'Shift+Enter', action: 'New line'),
    ];
    final mono = Theme.of(context).textTheme.bodyMedium?.copyWith(
      fontFamily: 'monospace',
      fontWeight: FontWeight.w600,
    );
    return AlertDialog(
      key: const Key('shortcut-sheet'),
      title: const Text('Keyboard shortcuts'),
      content: SizedBox(
        width: 460,
        child: SingleChildScrollView(
          child: Table(
            columnWidths: const {0: IntrinsicColumnWidth()},
            defaultVerticalAlignment: TableCellVerticalAlignment.middle,
            children: [
              for (final r in rows)
                TableRow(
                  children: [
                    Padding(
                      padding: const EdgeInsets.fromLTRB(0, 6, 24, 6),
                      child: Text(r.keys, style: mono),
                    ),
                    Padding(
                      padding: const EdgeInsets.symmetric(vertical: 6),
                      child: Text(r.action),
                    ),
                  ],
                ),
            ],
          ),
        ),
      ),
    );
  }
}
