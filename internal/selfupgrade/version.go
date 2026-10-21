package selfupgrade

import (
	"regexp"
	"strconv"
	"strings"
)

// A leading "v", or the `git describe --dirty` suffix: "-7-g17d66765" on a
// build made after a release tag, "-dirty" on one with local changes.
var versionDecoration = regexp.MustCompile(`^v|(-\d+-g[0-9a-f]+)?(-dirty)?$`)

// Newer reports whether latest is a newer release than current. Builds past a
// tag (`0.1.3-14-g17d66765`) count as that tag. Versions that don't parse
// (`dev`) fall back to plain inequality.
func Newer(latest, current string) bool {
	l, okL := parseVersion(latest)
	c, okC := parseVersion(current)
	if !okL || !okC {
		return latest != "" && latest != current
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parseVersion(s string) ([3]int, bool) {
	var v [3]int
	parts := strings.Split(versionDecoration.ReplaceAllString(s, ""), ".")
	if len(parts) != len(v) {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}
