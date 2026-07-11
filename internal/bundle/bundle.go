// Package bundle reads and writes the .argus session export archive: a gzipped
// tar of a manifest plus the session's raw tool files under a "root/" subtree.
package bundle

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// FormatVersion is the current bundle format. Read refuses anything newer.
const FormatVersion = 1

// manifestName is the tar entry holding the JSON manifest.
const manifestName = "manifest.json"

// Size caps guard against decompression bombs (a tiny gzip can declare a huge tar
// entry). Vars, not consts, so tests can shrink them.
var (
	maxManifestBytes int64 = 8 << 20 // 8 MiB
	maxBundleBytes   int64 = 4 << 30 // 4 GiB
)

// Metadata is the session header snapshot, captured at export time (repo/git
// context won't exist after extraction). Supplied by the client.
type Metadata struct {
	Title        string `json:"title,omitempty"`
	ModelName    string `json:"model_name,omitempty"`
	ModelColor   string `json:"model_color,omitempty"`
	Cwd          string `json:"cwd,omitempty"`
	Repo         string `json:"repo,omitempty"`
	GitUserName  string `json:"git_user_name,omitempty"`
	GitUserEmail string `json:"git_user_email,omitempty"`
	FirstMessage string `json:"first_message,omitempty"`
	Tokens       int    `json:"tokens,omitempty"`
	TurnCount    int    `json:"turn_count,omitempty"`
	DurationMs   int64  `json:"duration_ms,omitempty"`
	LastActivity string `json:"last_activity,omitempty"`
}

// Manifest is the bundle's descriptor.
type Manifest struct {
	FormatVersion int      `json:"format_version"`
	ArgusVersion  string   `json:"argus_version,omitempty"`
	ExportedAt    string   `json:"exported_at,omitempty"`
	Agent         string   `json:"agent"`
	Entry         string   `json:"entry"` // archive path of the main transcript
	Metadata      Metadata `json:"metadata"`
}

// SourceFile pairs a file's archive path with its source path on disk.
type SourceFile struct {
	ArchivePath string
	SourcePath  string
}

// errNotRegular marks a source path that isn't a plain regular file (symlink,
// dir, device). Such sidecars are skipped like a missing file; the entry is not.
var errNotRegular = errors.New("bundle: not a regular file")

// Write streams a gzipped tar of manifest + files to w.
func Write(w io.Writer, m Manifest, files []SourceFile) (err error) {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	// Close both on every path so a mid-loop error can't leak the writers.
	defer func() {
		if cerr := tw.Close(); err == nil {
			err = cerr
		}
		if cerr := gz.Close(); err == nil {
			err = cerr
		}
	}()

	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := writeTarBytes(tw, manifestName, mb); err != nil {
		return err
	}
	// Cap total payload (symmetric with Read) so a runaway session can't force an
	// unbounded archive into memory.
	remaining := maxBundleBytes
	for _, f := range files {
		if err := writeTarFile(tw, f, &remaining); err != nil {
			// Optional sidecars can vanish (or turn non-regular) in the window
			// between collection and write; skip them. The entry is required.
			if f.ArchivePath != m.Entry && (errors.Is(err, os.ErrNotExist) || errors.Is(err, errNotRegular)) {
				continue
			}
			return fmt.Errorf("bundle %s: %w", f.ArchivePath, err)
		}
	}
	return nil
}

func writeTarBytes(tw *tar.Writer, name string, b []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(b))}); err != nil {
		return err
	}
	_, err := tw.Write(b)
	return err
}

func writeTarFile(tw *tar.Writer, f SourceFile, remaining *int64) error {
	// Lstat, not Stat: a symlinked source would otherwise be archived with its
	// target's bytes, escaping the home scope the collectors enforce.
	info, err := os.Lstat(f.SourcePath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errNotRegular
	}
	if info.Size() > *remaining {
		return fmt.Errorf("bundle: payload exceeds %d bytes", maxBundleBytes)
	}
	src, err := os.Open(f.SourcePath)
	if err != nil {
		return err
	}
	defer src.Close()
	if err := tw.WriteHeader(&tar.Header{Name: f.ArchivePath, Mode: 0o644, Size: info.Size()}); err != nil {
		return err
	}
	// Cap at the size declared in the header: a live transcript may grow
	// between Lstat and Copy, which would otherwise trip "write too long".
	_, err = io.Copy(tw, io.LimitReader(src, info.Size()))
	*remaining -= info.Size()
	return err
}

// Read extracts a bundle's files into destDir. The manifest must be the first tar
// entry so its format_version is validated before any body is written; Read rejects
// a newer version and any entry escaping destDir. On error, destDir may hold
// partial files.
func Read(r io.Reader, destDir string) (Manifest, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return Manifest{}, fmt.Errorf("bundle: not a gzip archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	m, manifestBytes, err := readManifest(tr)
	if err != nil {
		return Manifest{}, err
	}
	if m.FormatVersion > FormatVersion {
		return Manifest{}, fmt.Errorf("bundle: format_version %d newer than supported %d (upgrade argus)", m.FormatVersion, FormatVersion)
	}
	if m.Entry == "" {
		return Manifest{}, fmt.Errorf("bundle: manifest has no entry")
	}

	remaining := maxBundleBytes
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return Manifest{}, fmt.Errorf("bundle: read tar: %w", err)
		}
		if err := extractEntry(destDir, hdr, tr, &remaining); err != nil {
			return Manifest{}, err
		}
	}

	// Persist the manifest so an already-extracted destDir can be reused without
	// re-reading the archive (see ReadManifest).
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return Manifest{}, err
	}
	if err := os.WriteFile(filepath.Join(destDir, manifestName), manifestBytes, 0o644); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// readManifest consumes the first tar entry, which must be the manifest, and
// returns it decoded alongside its raw bytes.
func readManifest(tr *tar.Reader) (Manifest, []byte, error) {
	hdr, err := tr.Next()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return Manifest{}, nil, fmt.Errorf("bundle: missing %s", manifestName)
		}
		return Manifest{}, nil, fmt.Errorf("bundle: read tar: %w", err)
	}
	if hdr.Name != manifestName {
		return Manifest{}, nil, fmt.Errorf("bundle: first entry is %q, want %s", hdr.Name, manifestName)
	}
	b, err := io.ReadAll(io.LimitReader(tr, maxManifestBytes+1))
	if err != nil {
		return Manifest{}, nil, err
	}
	if int64(len(b)) > maxManifestBytes {
		return Manifest{}, nil, fmt.Errorf("bundle: manifest exceeds %d bytes", maxManifestBytes)
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, nil, fmt.Errorf("bundle: bad manifest: %w", err)
	}
	return m, b, nil
}

// ReadManifest reads the manifest from an already-extracted bundle directory.
func ReadManifest(destDir string) (Manifest, error) {
	b, err := os.ReadFile(filepath.Join(destDir, manifestName))
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, fmt.Errorf("bundle: bad manifest: %w", err)
	}
	return m, nil
}

func extractEntry(destDir string, hdr *tar.Header, tr io.Reader, remaining *int64) error {
	// Bundle paths are always slash-separated and relative; a backslash or
	// volume name (C:\) only shows up in a traversal attempt.
	if strings.ContainsRune(hdr.Name, '\\') || filepath.VolumeName(hdr.Name) != "" {
		return fmt.Errorf("bundle: illegal path %q", hdr.Name)
	}
	clean := path.Clean(hdr.Name)
	if clean == ".." || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return fmt.Errorf("bundle: illegal path %q", hdr.Name)
	}
	target := filepath.Join(destDir, filepath.FromSlash(clean))
	switch hdr.Typeflag {
	case tar.TypeDir:
		return os.MkdirAll(target, 0o755)
	case tar.TypeReg:
	default:
		// Bundles only ever contain regular files and directories; reject symlinks,
		// hardlinks, and device nodes rather than silently materializing them.
		return fmt.Errorf("bundle: unsupported entry %q (type %d)", hdr.Name, hdr.Typeflag)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	n, err := io.Copy(out, io.LimitReader(tr, *remaining+1))
	*remaining -= n
	if err != nil {
		return err
	}
	if *remaining < 0 {
		return fmt.Errorf("bundle: extracted payload exceeds %d bytes", maxBundleBytes)
	}
	return nil
}
