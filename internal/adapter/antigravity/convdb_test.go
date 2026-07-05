package antigravity

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/transcript"
	_ "modernc.org/sqlite"
)

func TestExtractModelSlug(t *testing.T) {
	cases := map[string]string{
		"\x00gemini\x00gpt-oss-120b-medium\x00": "gpt-oss-120b-medium",
		"junk gemini-3.5-flash-low more":        "gemini-3.5-flash-low",
		"only gemini here":                      "",
		"":                                      "",
	}
	for blob, want := range cases {
		if got := extractModelSlug([]byte(blob)); got != want {
			t.Errorf("extractModelSlug(%q) = %q, want %q", blob, got, want)
		}
	}
}

func TestConvIDFromPath(t *testing.T) {
	p := "/home/u/.gemini/antigravity-cli/brain/conv-1/.system_generated/logs/transcript_full.jsonl"
	if got := convIDFromPath(p); got != "conv-1" {
		t.Errorf("convIDFromPath = %q, want conv-1", got)
	}
	if got := convIDFromPath("/tmp/x.jsonl"); got != "" {
		t.Errorf("convIDFromPath(no brain) = %q, want empty", got)
	}
}

func writeConvDB(t *testing.T, convID, modelSlug string) {
	t.Helper()
	path := conversationDBPath(convID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE executor_metadata (idx integer primary key, data blob)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO executor_metadata (idx, data) VALUES (0, ?)`, []byte("\x00gemini\x00"+modelSlug+"\x00")); err != nil {
		t.Fatal(err)
	}
}

func TestReadTranscriptViewStampsModel(t *testing.T) {
	setupHome(t)
	convID := "conv-stamp"
	writeConvDB(t, convID, "gpt-oss-120b-medium")
	writeBrainTranscript(t, convID, sampleTranscript)

	view, err := ReadTranscriptView(transcriptPathFor(convID))
	if err != nil {
		t.Fatal(err)
	}
	var ai *transcript.Chunk
	for i := range view.Chunks {
		if view.Chunks[i].Kind == transcript.ChunkAI {
			ai = &view.Chunks[i]
			break
		}
	}
	if ai == nil {
		t.Fatal("no AI chunk parsed")
	}
	if ai.ModelName != "gpt-oss-120b-medium" {
		t.Errorf("AI chunk model = %q, want gpt-oss-120b-medium", ai.ModelName)
	}
	if ai.ModelColor == "" {
		t.Error("AI chunk model color should be set")
	}
}

func TestConversationModel(t *testing.T) {
	setupHome(t)
	writeConvDB(t, "conv-m", "gpt-oss-120b-medium")

	name, color := conversationModel("conv-m")
	if name != "gpt-oss-120b-medium" {
		t.Errorf("name = %q, want gpt-oss-120b-medium", name)
	}
	if color == "" {
		t.Error("gpt family should resolve a color")
	}

	if n, _ := conversationModel("conv-missing"); n != "" {
		t.Errorf("missing db should yield empty, got %q", n)
	}
}
