package antigravity

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"
)

// modelSlugRe matches a model slug (family optionally followed by version/tier
// segments) inside a conversation-db metadata blob.
var modelSlugRe = regexp.MustCompile(`(?i)(?:gpt-oss|gpt|gemini|claude|grok|llama|deepseek|qwen|mistral)[a-z0-9.\-]*`)

// conversationModel reads the model from a conversation's sqlite db -- the only
// on-disk source (transcript_full.jsonl carries none). Uses executor_metadata
// (gen_metadata holds a placeholder enum for some families).
func conversationModel(convID string) (name, color string) {
	return conversationModelAt(conversationDBPath(convID))
}

// conversationModelIn reads the model from a conversation db under an explicit
// home (an extracted bundle), instead of the live home.
func conversationModelIn(home, convID string) (name, color string) {
	return conversationModelAt(conversationDBPathIn(home, convID))
}

func conversationModelAt(path string) (name, color string) {
	if path == "" {
		return "", ""
	}
	if _, err := os.Stat(path); err != nil {
		return "", ""
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(2000)&_pragma=query_only(true)")
	if err != nil {
		return "", ""
	}
	defer db.Close()

	rows, err := db.Query(`SELECT data FROM executor_metadata ORDER BY idx DESC`)
	if err != nil {
		return "", ""
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		if rows.Scan(&b) != nil {
			continue
		}
		if slug := extractModelSlug(b); slug != "" {
			return modelNameColor(slug)
		}
	}
	return "", ""
}

// extractModelSlug picks the most specific model slug from a metadata blob: the
// longest match carrying a version/tier ("-"/digit), so "gpt-oss-120b-medium"
// beats a bare "gpt" family word. "" when none is specific enough.
func extractModelSlug(blob []byte) string {
	best := ""
	for _, m := range modelSlugRe.FindAll(blob, -1) {
		s := string(m)
		if !hasVersion(s) {
			continue
		}
		if len(s) > len(best) {
			best = s
		}
	}
	return best
}

func hasVersion(s string) bool {
	for _, r := range s {
		if r == '-' || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}

var fileURIRe = regexp.MustCompile(`file://([^\x00-\x1f"]+)`)

// conversationWorkspace decodes the working directory from a conversation's
// trajectory_metadata_blob workspace URI.
func conversationWorkspace(convID string) string {
	path := conversationDBPath(convID)
	if path == "" {
		return ""
	}
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(2000)&_pragma=query_only(true)")
	if err != nil {
		return ""
	}
	defer db.Close()
	var blob []byte
	if err := db.QueryRow(`SELECT data FROM trajectory_metadata_blob LIMIT 1`).Scan(&blob); err != nil {
		return ""
	}
	m := fileURIRe.FindSubmatch(blob)
	if m == nil {
		return ""
	}
	return string(m[1])
}

// convIDFromPath extracts the conversation id from a brain transcript path
// (.../brain/<id>/.system_generated/...). "" when the path has no brain segment.
func convIDFromPath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for i, p := range parts {
		if p == "brain" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
