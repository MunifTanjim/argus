const _families = {'opus', 'sonnet', 'haiku', 'fable'};

/// Pretty-prints a Claude model id for display, e.g. `claude-opus-4-8` → `Opus
/// 4.8`. Version parts join with dots; 6+ digit date stamps are dropped and a
/// bracketed variant like `[1m]` is preserved. Unrecognized ids pass through.
String formatModelName(String raw) {
  var s = raw.trim();
  if (s.isEmpty) return raw;

  // Preserve variant tag such as [1m].
  final bracket = s.indexOf('[');
  final variant = bracket >= 0 ? s.substring(bracket) : '';
  if (bracket >= 0) s = s.substring(0, bracket);

  if (!s.startsWith('claude-')) return raw;

  String? family;
  final version = <String>[];
  for (final part in s.substring('claude-'.length).split('-')) {
    final lower = part.toLowerCase();
    if (_families.contains(lower)) {
      family = lower;
    } else if (RegExp(r'^\d+$').hasMatch(part) && part.length < 6) {
      version.add(part); // skip date stamps (>= 6 digits)
    }
  }
  if (family == null) return raw;

  final label = '${family[0].toUpperCase()}${family.substring(1)}'
      '${version.isEmpty ? '' : ' ${version.join('.')}'}';
  return variant.isEmpty ? label : '$label $variant';
}
