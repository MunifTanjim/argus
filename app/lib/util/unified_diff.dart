enum UdKind { hunk, context, add, del }

class UdLine {
  const UdLine(this.kind, this.text, {this.newNo, this.noEol = false});

  final UdKind kind;
  final String
  text; // without the +/-/space prefix; the whole @@ line for a hunk
  final int? newNo; // new-side line number; null for del and hunk rows
  final bool noEol;

  UdLine withNoEol() => UdLine(kind, text, newNo: newNo, noEol: true);
}

final _hunkHeader = RegExp(r'^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@');

/// Parses one file's unified diff. Lines outside a hunk (the diff/index/---/+++
/// headers) are skipped. Hunk line counts end a hunk, so a deleted line that
/// starts with "---" is not taken for a file header.
List<UdLine> parseUnifiedDiff(String diff) {
  final out = <UdLine>[];
  var oldLeft = 0, newLeft = 0, newNo = 0;
  for (final raw in diff.replaceAll('\r\n', '\n').split('\n')) {
    final m = _hunkHeader.firstMatch(raw);
    if (m != null) {
      oldLeft = int.parse(m[2] ?? '1');
      newLeft = int.parse(m[4] ?? '1');
      newNo = int.parse(m[3]!);
      out.add(UdLine(UdKind.hunk, raw));
      continue;
    }
    if (raw.startsWith('\\')) {
      if (out.isNotEmpty && out.last.kind != UdKind.hunk) {
        out[out.length - 1] = out.last.withNoEol();
      }
      continue;
    }
    if (oldLeft <= 0 && newLeft <= 0) continue;
    // Some tools strip the trailing space of an empty context line.
    final text = raw.isEmpty ? '' : raw.substring(1);
    switch (raw.isEmpty ? ' ' : raw[0]) {
      case ' ':
        out.add(UdLine(UdKind.context, text, newNo: newNo++));
        oldLeft--;
        newLeft--;
      case '+':
        out.add(UdLine(UdKind.add, text, newNo: newNo++));
        newLeft--;
      case '-':
        out.add(UdLine(UdKind.del, text));
        oldLeft--;
    }
  }
  return out;
}
