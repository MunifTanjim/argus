import 'package:pub_semver/pub_semver.dart';

/// Oldest argus server this app works with. Older servers get a blocking
/// upgrade notice (see unsupported_server_screen.dart).
const minServerVersion = '0.1.2';

/// Oldest argus node this app works with. Older nodes stay visible but are
/// flagged outdated and lose their actions.
const minNodeVersion = '0.1.3';

/// A leading "v", or the `git describe --dirty` suffix: "-7-g17d66765" on a
/// build made after a release tag, "-dirty" on one with local changes.
final _decoration = RegExp(r'^v|(-\d+-g[0-9a-f]+)?(-dirty)?$');

final _parsedMins = <String, Version>{};

Version? _parse(String version) {
  try {
    return Version.parse(version.replaceAll(_decoration, ''));
  } on FormatException {
    return null;
  }
}

/// Whether [version] meets [min]. Builds past a tag (`0.1.2-7-g17d66765`)
/// count as that tag, unlike semver which ranks them below it. Versions that
/// don't parse (`dev`, empty) pass: there is nothing to compare.
bool isSupported(String version, String min) {
  final v = _parse(version);
  return v == null || v >= _parsedMins.putIfAbsent(min, () => Version.parse(min));
}

/// Whether [latest] is a newer release than [current], with the same build
/// rules as [isSupported]. Versions that don't parse fall back to inequality.
bool upgradeAvailable(String current, String latest) {
  final l = _parse(latest), c = _parse(current);
  if (l == null || c == null) return latest.isNotEmpty && latest != current;
  return l > c;
}
