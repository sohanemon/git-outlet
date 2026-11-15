package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// NOTE: Repo records tool ownership so safety checks can distinguish adopted repos.
type Repo struct {
	Owner  string `json:"owner"`
	Name   string `json:"name"`
	Commit string `json:"commit"`
}

// NOTE: Entry records one publication; Gist and Repo are mutually exclusive.
type Entry struct {
	GistID string `json:"gist_id,omitempty"`
	Hash   string `json:"hash,omitempty"`
	Repo   *Repo  `json:"repo,omitempty"`
}

// NOTE: State maps outlet entry names to their recorded publication.
type State map[string]Entry

// NOTE: Load reads the state file, returning an empty state when it does not exist.
func Load(path string) (State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state %s: %w", path, err)
	}

	st := State{}
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", path, err)
	}

	return st, nil
}

// NOTE: Save writes the state file atomically with stable indentation.
func Save(path string, st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".git-outlet-state-*.tmp")
	if err != nil {
		return fmt.Errorf("create state file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write state %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close state %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace state %s: %w", path, err)
	}

	return nil
}

// NOTE: Get returns the recorded entry for name.
func (s State) Get(name string) (Entry, bool) {
	entry, ok := s[name]

	return entry, ok
}

// NOTE: Set records entry for name.
func (s State) Set(name string, entry Entry) {
	s[name] = entry
}
