package selfupgrade

import (
	"regexp"

	"golang.org/x/mod/semver"
)

// A leading "v", or the `git describe --dirty` suffix: "-7-g17d66765" on a
// build made after a release tag, "-dirty" on one with local changes.
var versionDecoration = regexp.MustCompile(`^v|(-\d+-g[0-9a-f]+)?(-dirty)?$`)

// Newer reports whether latest is a newer release than current, using semver
// precedence: `0.1.3 < 0.1.4-beta.1 < 0.1.4-beta.2 < 0.1.4`. Builds past a tag
// (`0.1.3-14-g17d66765`) count as that tag. Versions that don't parse (`dev`)
// fall back to plain inequality.
func Newer(latest, current string) bool {
	l, c := canonicalVersion(latest), canonicalVersion(current)
	if l == "" || c == "" {
		return latest != "" && latest != current
	}
	return semver.Compare(l, c) > 0
}

// IsPrerelease reports whether v parses as a version with a prerelease part.
func IsPrerelease(v string) bool {
	return semver.Prerelease(canonicalVersion(v)) != ""
}

// canonicalVersion returns s as a full "vMAJOR.MINOR.PATCH[-PRE]" semver, or
// "" if s is not one.
func canonicalVersion(s string) string {
	v := "v" + versionDecoration.ReplaceAllString(s, "")
	if semver.Canonical(v) != v {
		return ""
	}
	return v
}
