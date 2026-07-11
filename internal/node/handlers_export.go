package node

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bundle"
	"github.com/MunifTanjim/argus/internal/gitmeta"
)

func (d *Node) handleExportBundle(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.ExportBundleParams](params)
	if err != nil {
		return nil, err
	}
	if p.TranscriptPath == "" {
		return nil, fmt.Errorf("exportBundle: transcript_path is required")
	}
	a, ok := d.adapters[p.Agent]
	if !ok {
		return nil, fmt.Errorf("exportBundle: unknown agent %q", p.Agent)
	}
	files, err := a.CollectSessionFiles(p.TranscriptPath)
	if err != nil {
		return nil, fmt.Errorf("exportBundle: collect: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("exportBundle: no files collected for %s", p.TranscriptPath)
	}

	entry := ""
	srcs := make([]bundle.SourceFile, 0, len(files))
	for _, f := range files {
		srcs = append(srcs, bundle.SourceFile{ArchivePath: f.RelPath, SourcePath: f.AbsPath})
		if filepath.Clean(f.AbsPath) == filepath.Clean(p.TranscriptPath) {
			entry = f.RelPath
		}
	}
	if entry == "" {
		entry = files[0].RelPath // fallback: first file is the transcript
	}

	m := bundle.Manifest{
		FormatVersion: bundle.FormatVersion,
		ArgusVersion:  d.version,
		ExportedAt:    time.Now().UTC().Format(time.RFC3339),
		Agent:         a.Agent(),
		Entry:         entry,
		Metadata:      p.Metadata,
	}
	m.Metadata.GitUserName, m.Metadata.GitUserEmail = gitmeta.Identity(ctx, p.Metadata.Cwd)

	var buf bytes.Buffer
	if err := bundle.Write(&buf, m, srcs); err != nil {
		return nil, fmt.Errorf("exportBundle: write: %w", err)
	}
	return api.ExportBundleResult{
		Filename: suggestFilename(a.Agent(), p.TranscriptPath, p.Metadata),
		Data:     buf.Bytes(),
	}, nil
}

// suggestFilename builds "<agent>--<repo>--<session-id>.argus"; repo is omitted
// when unknown.
func suggestFilename(agent, transcriptPath string, md bundle.Metadata) string {
	label := agent
	if md.Repo != "" {
		label += "--" + md.Repo
	}
	id := strings.TrimSuffix(filepath.Base(transcriptPath), ".jsonl")
	label = sanitizeFilenamePart(label)
	if id == "" {
		return label + ".argus"
	}
	return label + "--" + sanitizeFilenamePart(id) + ".argus"
}

func sanitizeFilenamePart(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == ' ' || r == filepath.Separator {
			return '-'
		}
		return r
	}, s)
}
