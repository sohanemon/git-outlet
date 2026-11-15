package state

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	st, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(st) != 0 {
		t.Errorf("Load() = %v, want an empty state", st)
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	want := State{
		"helpers.js": {GistID: "abc123", Hash: "deadbeef"},
		"notes.md":   {GistID: "def456", Hash: "cafebabe"},
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"helpers.js":`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("Load() error = %v, want it to name %s", err, path)
	}
}

func TestSaveStableAndAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	st := State{
		"b.md": {GistID: "2", Hash: "h2"},
		"a.md": {GistID: "1", Hash: "h1"},
	}

	if err := Save(path, st); err != nil {
		t.Fatalf("Save() error: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := Save(path, st); err != nil {
		t.Fatalf("Save() second error: %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first, second) {
		t.Errorf("Save() output changed between runs:\n%s\n%s", first, second)
	}
	if !strings.Contains(string(first), "\n  \"a.md\"") {
		t.Errorf("Save() output is not indented sorted JSON:\n%s", first)
	}
	if !strings.HasSuffix(string(first), "\n") {
		t.Errorf("Save() output does not end with a newline: %q", first)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Errorf("directory entries = %v, want only state.json", entries)
	}
}
