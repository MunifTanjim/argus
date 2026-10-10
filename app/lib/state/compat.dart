import 'package:pub_semver/pub_semver.dart';

/// Oldest argus server this app works with. Older servers get a blocking
/// upgrade notice (see unsupported_server_screen.dart).
const minServerVersion = '0.1.2';

/// Oldest argus node this app works with. Older nodes stay visible but are
/// flagged outdated and lose their actions.
const minNodeVersion = '0.1.2';

/// A leading "v", or the `git describe --dirty` suffix: "-7-g17d66765" on a
/// build made after a release tag, "-dirty" on one with local changes.
final _decoration = RegExp(r'^v|(-\d+-g[0-9a-f]+)?(-dirty)?$');

final _parsedMins = <String, Version>{};

/// Whether [version] meets [min]. Builds past a tag (`0.1.2-7-g17d66765`)
/// count as that tag, unlike semver which ranks them below it. Versions that
/// don't parse (`dev`, empty) pass: there is nothing to compare.
bool isSupported(String version, String min) {
  final Version v;
  try {
    v = Version.parse(version.replaceAll(_decoration, ''));
  } on FormatException {
    return true;
  }
  return v >= _parsedMins.putIfAbsent(min, () => Version.parse(min));
}
