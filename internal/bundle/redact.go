package bundle

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	_ "modernc.org/sqlite"
)

// RedactPlaceholder replaces every redacted span in a saved bundle.
const RedactPlaceholder = "[REDACTED]"

type fileKind int

const (
	kindText fileKind = iota
	kindSQLite
	kindOtherBinary
)

var sqliteMagic = []byte("SQLite format 3\x00")

// classifyFile sniffs the first 512 bytes: sqlite magic → db, NUL or invalid
// UTF-8 → binary, else text. A file that changes character past 512 bytes can be
// misclassified; RedactTree's residual scan is the backstop.
func classifyFile(path string) (fileKind, error) {
	f, err := os.Open(path)
	if err != nil {
		return kindText, err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, err := f.Read(head)
	if err != nil && err != io.EOF {
		return kindText, err
	}
	head = head[:n]
	if bytes.HasPrefix(head, sqliteMagic) {
		return kindSQLite, nil
	}
	if bytes.IndexByte(head, 0) >= 0 || !utf8.Valid(head) {
		return kindOtherBinary, nil
	}
	return kindText, nil
}

// litVariant is a byte-sequence to search for, attributed to the literal it counts
// toward (one secret expands into several variants; see expandLiterals).
type litVariant struct {
	match []byte
	owner string
}

// jsonEscapedForms returns s as it appears inside a JSON string (both HTML-escaping
// modes), dropping forms equal to s. Transcripts are JSONL, so a secret with " \ <
// > or & is stored escaped and won't match the raw literal.
func jsonEscapedForms(s string) []string {
	var out []string
	add := func(v string) {
		if len(v) > 0 && v != s {
			out = append(out, v)
		}
	}
	if b, err := json.Marshal(s); err == nil && len(b) >= 2 {
		add(string(b[1 : len(b)-1])) // strip the surrounding quotes (HTML-escaped)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err == nil {
		if t := strings.TrimRight(buf.String(), "\n"); len(t) >= 2 {
			add(t[1 : len(t)-1])
		}
	}
	return out
}

// expandLiterals returns each literal plus its JSON-escaped forms as byte-variants,
// sorted longest-first so a shorter variant can't clobber a longer overlapping one.
// Duplicate byte-sequences are dropped so a shared variant isn't counted twice.
func expandLiterals(literals []string) []litVariant {
	var out []litVariant
	seen := make(map[string]bool)
	add := func(match, owner string) {
		if match == "" || seen[match] {
			return
		}
		seen[match] = true
		out = append(out, litVariant{match: []byte(match), owner: owner})
	}
	for _, lit := range literals {
		if lit == "" {
			continue
		}
		add(lit, lit)
		for _, v := range jsonEscapedForms(lit) {
			add(v, lit)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return len(out[i].match) > len(out[j].match) })
	return out
}

// redactCell replaces each variant in b with RedactPlaceholder, counting hits
// against the owning literal. variants must be longest-first. Reports if changed.
func redactCell(b []byte, variants []litVariant, counts map[string]int) ([]byte, bool) {
	changed := false
	for _, v := range variants {
		n := bytes.Count(b, v.match)
		if n == 0 {
			continue
		}
		counts[v.owner] += n
		b = bytes.ReplaceAll(b, v.match, []byte(RedactPlaceholder))
		changed = true
	}
	return b, changed
}

// redactTextBytes replaces every literal (and its JSON-escaped forms) with
// RedactPlaceholder, returning the rewritten bytes and per-literal counts.
func redactTextBytes(b []byte, literals []string) ([]byte, map[string]int) {
	counts := make(map[string]int, len(literals))
	out, _ := redactCell(b, expandLiterals(literals), counts)
	return out, counts
}

type sqliteEdit struct {
	table string
	col   string
	rowid int64
	val   []byte
}

// walkSQLite calls fn for every non-nil cell of every rowid table; it never
// mutates. Rowid-less tables (WITHOUT ROWID, virtual) are skipped and returned so
// callers can warn.
func walkSQLite(db *sql.DB, fn func(table, col string, rowid int64, val []byte)) ([]string, error) {
	tables, err := sqliteTables(db)
	if err != nil {
		return nil, err
	}
	var skipped []string
	for _, tbl := range tables {
		cols, hasRowid, err := sqliteColumns(db, tbl)
		if err != nil {
			return nil, err
		}
		if !hasRowid {
			skipped = append(skipped, tbl)
			continue
		}
		if len(cols) == 0 {
			continue
		}
		sel := `SELECT rowid`
		for _, c := range cols {
			sel += `, "` + c + `"`
		}
		sel += ` FROM "` + tbl + `"`
		rows, err := db.Query(sel)
		if err != nil {
			return nil, err
		}
		dest := make([]any, len(cols)+1)
		var rowid int64
		dest[0] = &rowid
		raw := make([]sql.RawBytes, len(cols))
		for i := range raw {
			dest[i+1] = &raw[i]
		}
		for rows.Next() {
			if err := rows.Scan(dest...); err != nil {
				rows.Close()
				return nil, err
			}
			for i, c := range cols {
				if raw[i] == nil {
					continue
				}
				val := append([]byte(nil), raw[i]...) // copy before Next invalidates it
				fn(tbl, c, rowid, val)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return skipped, nil
}

func sqliteTables(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func sqliteColumns(db *sql.DB, table string) (cols []string, hasRowid bool, err error) {
	rows, err := db.Query(`PRAGMA table_info("` + table + `")`)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid, notnull, pk int
			name, ctype      string
			dflt             sql.NullString
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return nil, false, err
		}
		cols = append(cols, name)
	}
	// A plain table (not WITHOUT ROWID) always answers a rowid query; probe it.
	var probe int64
	if e := db.QueryRow(`SELECT rowid FROM "` + table + `" LIMIT 1`).Scan(&probe); e == nil || e == sql.ErrNoRows {
		hasRowid = true
	}
	return cols, hasRowid, rows.Err()
}

func scanSQLite(path string, literals []string) (map[string]int, []string, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=query_only(true)")
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()
	counts := make(map[string]int, len(literals))
	sorted := expandLiterals(literals)
	skipped, err := walkSQLite(db, func(_, _ string, _ int64, val []byte) {
		// Count on a mutated copy so overlapping literals count as redaction would.
		redactCell(val, sorted, counts)
	})
	return counts, skipped, err
}

func redactSQLite(path string, literals []string) (map[string]int, []string, error) {
	// journal_mode=DELETE converts a WAL db and drops -wal/-shm sidecars, so no
	// secret-bearing sidecar is left for WriteDir to zip.
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(DELETE)")
	if err != nil {
		return nil, nil, err
	}
	defer db.Close()
	counts := make(map[string]int, len(literals))
	sorted := expandLiterals(literals)
	var edits []sqliteEdit
	skipped, err := walkSQLite(db, func(table, col string, rowid int64, val []byte) {
		if out, changed := redactCell(val, sorted, counts); changed {
			edits = append(edits, sqliteEdit{table, col, rowid, out})
		}
	})
	if err != nil {
		return nil, nil, err
	}
	if len(edits) > 0 {
		tx, err := db.Begin()
		if err != nil {
			return nil, nil, err
		}
		for _, e := range edits {
			if _, err := tx.Exec(fmt.Sprintf(`UPDATE "%s" SET "%s"=? WHERE rowid=?`, e.table, e.col), e.val, e.rowid); err != nil {
				tx.Rollback()
				return nil, nil, err
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, nil, err
		}
	}
	// VACUUM unconditionally (can't run in a transaction): a freed page from a
	// pre-export delete may still hold a secret that only a rebuild reclaims.
	if _, err := db.Exec(`VACUUM`); err != nil {
		return nil, nil, err
	}
	return counts, skipped, nil
}

// Report summarizes a redaction pass: per-literal occurrence counts and warnings
// for content that could not be safely redacted (e.g. secrets in binary files).
type Report struct {
	Counts   map[string]int
	Warnings []string
}

// ZeroMatch returns the queried literals that matched nothing anywhere.
func (r Report) ZeroMatch(literals []string) []string {
	var out []string
	for _, lit := range literals {
		if r.Counts[lit] == 0 {
			out = append(out, lit)
		}
	}
	return out
}

func mergeCounts(dst, src map[string]int) {
	for k, v := range src {
		dst[k] += v
	}
}

// redactMetadata scrubs every string field of the manifest metadata by reflection,
// so a newly-added string field is scrubbed automatically rather than missed.
func redactMetadata(m Metadata, literals []string) (Metadata, map[string]int) {
	counts := make(map[string]int)
	v := reflect.ValueOf(&m).Elem()
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() != reflect.String || !f.CanSet() {
			continue
		}
		out, c := redactTextBytes([]byte(f.String()), literals)
		mergeCounts(counts, c)
		f.SetString(string(out))
	}
	return m, counts
}

// sqliteSkipWarning describes a table that could not be redacted in place.
func sqliteSkipWarning(rel, tbl string) string {
	return fmt.Sprintf(
		"sqlite table %q in %s not redactable (no rowid / virtual table); manual review needed",
		tbl, filepath.ToSlash(rel))
}

// binarySecretWarning describes a secret found in a binary file that cannot be
// redacted without corrupting it.
func binarySecretWarning(rel string) string {
	return "secret in binary file " + filepath.ToSlash(rel) + " (cannot redact)"
}

// binaryContainsLiteral reports whether any literal, or a JSON-escaped form of one,
// appears in b — recognizing the same forms redaction removes.
func binaryContainsLiteral(b []byte, literals []string) bool {
	for _, v := range expandLiterals(literals) {
		if bytes.Contains(b, v.match) {
			return true
		}
	}
	return false
}

// residualWarning describes a secret found in a file after the redaction pass —
// a leak the structured redaction did not catch.
func residualWarning(rel string) string {
	return "secret survives in " + filepath.ToSlash(rel) + " after redaction (manual review needed)"
}

// verifyResiduals raw-scans every written file for surviving literals, catching
// what the structured pass missed (sqlite freed pages, stray sidecars). Files in
// skip (already flagged by a specific warning) aren't reported again.
func verifyResiduals(dir string, literals []string, skip map[string]bool) ([]string, error) {
	var warnings []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if skip[filepath.ToSlash(rel)] {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if binaryContainsLiteral(b, literals) {
			warnings = append(warnings, residualWarning(rel))
		}
		return nil
	})
	return warnings, err
}

// Scan previews what a redaction of srcDir would replace, without writing. It reads
// live sqlite cells only and runs no residual backstop, so it under-reports leaks;
// never gate a save on Scan alone — RedactTree's Report is authoritative.
func Scan(srcDir string, literals []string) (Report, error) {
	rep := Report{Counts: make(map[string]int)}
	m, err := ReadManifest(srcDir)
	if err != nil {
		return rep, err
	}
	_, mc := redactMetadata(m.Metadata, literals)
	mergeCounts(rep.Counts, mc)

	err = filepath.WalkDir(srcDir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Base(p) == manifestName {
			return nil // metadata already counted from the parsed manifest
		}
		kind, err := classifyFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(srcDir, p)
		switch kind {
		case kindText:
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			_, c := redactTextBytes(b, literals)
			mergeCounts(rep.Counts, c)
		case kindSQLite:
			c, skipped, serr := scanSQLite(p, literals)
			if serr != nil {
				return serr
			}
			mergeCounts(rep.Counts, c)
			for _, tbl := range skipped {
				rep.Warnings = append(rep.Warnings, sqliteSkipWarning(rel, tbl))
			}
		case kindOtherBinary:
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			if binaryContainsLiteral(b, literals) {
				rep.Warnings = append(rep.Warnings, binarySecretWarning(rel))
			}
		}
		return nil
	})
	return rep, err
}

// RedactTree copies srcDir into dstDir, replacing every literal in text files,
// sqlite dbs, and the manifest metadata. Binary files that contain a secret are
// copied unchanged and recorded as warnings. dstDir is created fresh.
func RedactTree(srcDir, dstDir string, literals []string) (Report, error) {
	rep := Report{Counts: make(map[string]int)}
	m, err := ReadManifest(srcDir)
	if err != nil {
		return rep, err
	}
	redactedMeta, mc := redactMetadata(m.Metadata, literals)
	mergeCounts(rep.Counts, mc)
	m.Metadata = redactedMeta

	// Files already flagged by a specific warning are excluded from the residual
	// backstop so each leak is reported once.
	warned := make(map[string]bool)

	err = filepath.WalkDir(srcDir, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, err := filepath.Rel(srcDir, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if filepath.Base(p) == manifestName {
			mb, err := json.MarshalIndent(m, "", "  ")
			if err != nil {
				return err
			}
			return os.WriteFile(target, mb, 0o644)
		}
		kind, err := classifyFile(p)
		if err != nil {
			return err
		}
		switch kind {
		case kindText:
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			out, c := redactTextBytes(b, literals)
			mergeCounts(rep.Counts, c)
			return os.WriteFile(target, out, 0o644)
		case kindSQLite:
			if err := copyFile(p, target); err != nil {
				return err
			}
			c, skipped, err := redactSQLite(target, literals)
			if err != nil {
				return err
			}
			mergeCounts(rep.Counts, c)
			for _, tbl := range skipped {
				rep.Warnings = append(rep.Warnings, sqliteSkipWarning(rel, tbl))
				warned[filepath.ToSlash(rel)] = true
			}
			return nil
		default: // kindOtherBinary
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if binaryContainsLiteral(b, literals) {
				rep.Warnings = append(rep.Warnings, binarySecretWarning(rel))
				warned[filepath.ToSlash(rel)] = true
			}
			return os.WriteFile(target, b, 0o644)
		}
	})
	if err != nil {
		return rep, err
	}

	// Backstop: raw-scan the written tree for anything the structured pass missed.
	residuals, err := verifyResiduals(dstDir, literals, warned)
	rep.Warnings = append(rep.Warnings, residuals...)
	return rep, err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
