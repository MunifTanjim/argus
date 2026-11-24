import 'dart:io';

import 'package:flutter_test/flutter_test.dart';

void main() {
  test('no AppBar is constructed directly in lib/', () {
    final pattern = RegExp(r'(?<![A-Za-z])AppBar\(');
    final found = <String>[];
    for (final entity in Directory('lib').listSync(recursive: true)) {
      if (entity is! File || !entity.path.endsWith('.dart')) continue;
      if (entity.path.endsWith('ui/titlebar.dart')) continue;
      final lines = entity.readAsLinesSync();
      for (var i = 0; i < lines.length; i++) {
        final line = lines[i];
        if (line.trimLeft().startsWith('//')) continue;
        if (pattern.hasMatch(line)) found.add('${entity.path}:${i + 1}');
      }
    }
    expect(found, isEmpty, reason: 'use ArgusAppBar instead of AppBar');
  });
}
