package tui

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGroupByStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "argus", "tui.json")
	if g, err := loadGroupBy(path); err != nil || g != groupByHost {
		t.Fatalf("missing file = %q, %v; want host, nil", g, err)
	}
	if err := saveGroupBy(path, groupByProject); err != nil {
		t.Fatal(err)
	}
	if g, err := loadGroupBy(path); err != nil || g != groupByProject {
		t.Fatalf("after save = %q, %v; want project", g, err)
	}
	data, _ := os.ReadFile(path)
	var doc struct {
		Sessions struct {
			GroupBy string `json:"group_by"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.Sessions.GroupBy != "project" {
		t.Fatalf("file shape: %s", data)
	}
}

func TestGroupByStateFallbacks(t *testing.T) {
	for name, body := range map[string]string{
		"invalid json":   `{`,
		"unknown value":  `{"sessions":{"group_by":"folder"}}`,
		"wrong type":     `{"sessions":"x"}`,
		"top-level list": `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tui.json")
			os.WriteFile(path, []byte(body), 0o600)
			g, err := loadGroupBy(path)
			if g != groupByHost || err == nil {
				t.Fatalf("got %q, %v; want host with an error", g, err)
			}
		})
	}
	t.Run("empty sessions", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "tui.json")
		os.WriteFile(path, []byte(`{}`), 0o600)
		if g, err := loadGroupBy(path); g != groupByHost || err != nil {
			t.Fatalf("got %q, %v; want host, nil", g, err)
		}
	})
}

func TestSaveGroupByKeepsOtherKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tui.json")
	os.WriteFile(path, []byte(`{"other":{"x":1},"sessions":{"group_by":"agent","keep":true}}`), 0o600)
	if err := saveGroupBy(path, groupByStatus); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	var doc map[string]map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["other"]["x"] != float64(1) || doc["sessions"]["keep"] != true || doc["sessions"]["group_by"] != "status" {
		t.Fatalf("unexpected file: %s", data)
	}
}

func TestSaveGroupByReplacesBadFile(t *testing.T) {
	for _, body := range []string{`null`, `[]`, `{`, `{"sessions":"x"}`} {
		path := filepath.Join(t.TempDir(), "tui.json")
		os.WriteFile(path, []byte(body), 0o600)
		if err := saveGroupBy(path, groupByAgent); err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if g, err := loadGroupBy(path); g != groupByAgent || err != nil {
			t.Fatalf("%s: got %q, %v", body, g, err)
		}
	}
}

func TestLeftTabStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "argus", "tui.json")
	if tab, err := loadLeftTab(path); err != nil || tab != leftProjects {
		t.Fatalf("missing file = %v, %v; want projects, nil", tab, err)
	}
	if err := saveGroupBy(path, groupByProject); err != nil {
		t.Fatal(err)
	}
	if err := saveLeftTab(path, leftSessions); err != nil {
		t.Fatal(err)
	}
	if tab, err := loadLeftTab(path); err != nil || tab != leftSessions {
		t.Fatalf("after save = %v, %v; want sessions", tab, err)
	}
	if g, err := loadGroupBy(path); err != nil || g != groupByProject {
		t.Fatalf("group_by lost after saving the tab: %q, %v", g, err)
	}
	data, _ := os.ReadFile(path)
	var doc struct {
		LeftSidebar struct {
			Tab string `json:"tab"`
		} `json:"left_sidebar"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.LeftSidebar.Tab != "sessions" {
		t.Fatalf("file shape: %s", data)
	}
}

func TestLeftTabStateFallbacks(t *testing.T) {
	for name, body := range map[string]string{
		"invalid json":  `{`,
		"unknown value": `{"left_sidebar":{"tab":"files"}}`,
		"wrong type":    `{"left_sidebar":"x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "tui.json")
			os.WriteFile(path, []byte(body), 0o600)
			tab, err := loadLeftTab(path)
			if tab != leftProjects || err == nil {
				t.Fatalf("got %v, %v; want projects with an error", tab, err)
			}
		})
	}
	t.Run("absent key", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "tui.json")
		os.WriteFile(path, []byte(`{"sessions":{"group_by":"agent"}}`), 0o600)
		if tab, err := loadLeftTab(path); tab != leftProjects || err != nil {
			t.Fatalf("got %v, %v; want projects, nil", tab, err)
		}
	})
}
