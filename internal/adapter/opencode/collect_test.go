package opencode

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
)

func withServiceInfo(t *testing.T, url string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "service.json")
	if err := os.WriteFile(path, []byte(`{"url":"`+url+`","password":"secret"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	old := serviceInfoPath
	serviceInfoPath = path
	t.Cleanup(func() { serviceInfoPath = old })
}

func TestCollectSessionFilesWritesTranscript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/session/ses_1/message" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"data":[
			{"type":"user","id":"m1","time":{"created":1},"text":"hello"},
			{"type":"assistant","id":"m2","time":{"created":2},"content":[{"type":"text","id":"p1","text":"hi"}]}
		],"cursor":{"previous":"","next":""}}`))
	}))
	defer srv.Close()
	withServiceInfo(t, srv.URL)

	files, err := collectSessionFiles("ses_1")
	if err != nil {
		t.Fatalf("collectSessionFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d: %+v", len(files), files)
	}
	f := files[0]
	defer os.Remove(f.AbsPath)

	if f.RelPath != adapter.BundleRoot+"/transcript.json" {
		t.Fatalf("RelPath = %q", f.RelPath)
	}
	data, err := os.ReadFile(f.AbsPath)
	if err != nil {
		t.Fatalf("bundled file not on disk: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, `"id": "m1"`) || !strings.Contains(body, `"id": "m2"`) {
		t.Fatalf("transcript missing messages: %s", body)
	}
}

func TestCollectSessionFilesServiceDownErrors(t *testing.T) {
	old := serviceInfoPath
	serviceInfoPath = filepath.Join(t.TempDir(), "missing.json")
	t.Cleanup(func() { serviceInfoPath = old })

	if _, err := collectSessionFiles("ses_1"); err == nil {
		t.Fatal("want an error when the service is unavailable, got nil")
	}
}
