import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:argus/models/enums.dart';
import 'package:argus/ui/status_style.dart';

void main() {
  test('glyphs by status', () {
    expect(statusGlyph(SessionStatus.working), '●');
    expect(statusGlyph(SessionStatus.awaitingInput), '◆');
    expect(statusGlyph(SessionStatus.idle), '○');
    expect(statusGlyph(SessionStatus.dead), '×');
    expect(statusGlyph(SessionStatus.unknown), '·');
  });

  test('colors by status', () {
    expect(statusColor(SessionStatus.working), const Color(0xFFb8bb26));
    expect(statusColor(SessionStatus.awaitingInput), const Color(0xFFfabd2f));
    expect(statusColor(SessionStatus.idle), const Color(0xFF928374));
    expect(statusColor(SessionStatus.dead), const Color(0xFFfb4934));
  });

}
