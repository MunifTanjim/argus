import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

// Exact lines that parse a user-supplied hex string; the mask only applies
// full alpha, so they are not palette colors.
const _parsedHex = {
  ('lib/ui/spawn_dialog.dart', 'return Color(0xFF000000 | v);'),
  ('lib/ui/usage_format.dart', 'return v == null ? null : Color(0xFF000000 | v);'),
};

void main() {
  test('no color literals outside lib/ui/theme', () {
    final offenders = <String>[];
    final re = RegExp(
        r'Color\.from|Color\(0x|CupertinoColors|Colors\.(?!transparent\b)[a-z]');
    for (final f
        in Directory('lib').listSync(recursive: true).whereType<File>()) {
      if (!f.path.endsWith('.dart') ||
          f.path.startsWith('lib/ui/theme/')) {
        continue;
      }
      final lines = f.readAsLinesSync();
      for (var i = 0; i < lines.length; i++) {
        final text = lines[i].trim();
        if (text.startsWith('//') || !re.hasMatch(text)) continue;
        if (_parsedHex.contains((f.path, text))) continue;
        offenders.add('${f.path}:${i + 1}');
      }
    }
    expect(offenders, isEmpty);
  });
}
