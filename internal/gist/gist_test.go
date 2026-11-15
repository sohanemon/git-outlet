package gist

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls int
	args  []string
	stdin string
	out   string
	err   error
}

func (f *fakeRunner) Run(_ context.Context, args []string, stdin string) (string, error) {
	f.calls++
	f.args = args
	f.stdin = stdin

	return f.out, f.err
}

func newResponse(id, url string) string {
	return `{"id":"` + id + `","html_url":"` + url + `"}`
}

func TestCreate(t *testing.T) {
	runner := &fakeRunner{out: newResponse("abc123", "https://gist.github.com/abc123")}
	client := New(runner)

	id, url, err := client.Create(context.Background(), "notes.md", "desc", "body")
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if id != "abc123" || url != "https://gist.github.com/abc123" {
		t.Errorf("Create() = %q, %q", id, url)
	}

	wantArgs := []string{"api", "--method", "POST", "gists", "--input", "-"}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Errorf("Create() args = %v, want %v", runner.args, wantArgs)
	}

	var body struct {
		Description string `json:"description"`
		Public      *bool  `json:"public"`
		Files       map[string]struct {
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(runner.stdin), &body); err != nil {
		t.Fatalf("Create() stdin is not JSON: %v", err)
	}
	if body.Description != "desc" {
		t.Errorf("description = %q, want desc", body.Description)
	}
	if body.Public == nil || !*body.Public {
		t.Errorf("public = %v, want true", body.Public)
	}
	if got := body.Files["notes.md"].Content; got != "body" {
		t.Errorf("content = %q, want body", got)
	}
}

func TestCreateDefaultsDescriptionAndAllowsEmptyContent(t *testing.T) {
	runner := &fakeRunner{out: newResponse("abc123", "https://gist.github.com/abc123")}
	client := New(runner)

	if _, _, err := client.Create(context.Background(), "notes.md", "", ""); err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	var body struct {
		Description string `json:"description"`
		Files       map[string]struct {
			Content string `json:"content"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(runner.stdin), &body); err != nil {
		t.Fatalf("Create() stdin is not JSON: %v", err)
	}
	if body.Description != "notes.md" {
		t.Errorf("description = %q, want notes.md", body.Description)
	}
	if got, ok := body.Files["notes.md"]; !ok || got.Content != "" {
		t.Errorf("files = %v, want an empty notes.md content", body.Files)
	}
}

func TestUpdate(t *testing.T) {
	runner := &fakeRunner{out: newResponse("abc123", "https://gist.github.com/abc123")}
	client := New(runner)

	url, err := client.Update(context.Background(), "abc123", "notes.md", "desc", "body")
	if err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	if url != "https://gist.github.com/abc123" {
		t.Errorf("Update() url = %q", url)
	}

	wantArgs := []string{"api", "--method", "PATCH", "gists/abc123", "--input", "-"}
	if !reflect.DeepEqual(runner.args, wantArgs) {
		t.Errorf("Update() args = %v, want %v", runner.args, wantArgs)
	}

	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(runner.stdin), &body); err != nil {
		t.Fatalf("Update() stdin is not JSON: %v", err)
	}
	if _, ok := body["public"]; ok {
		t.Error("Update() body contains public, want it omitted")
	}
}

func TestNotFound(t *testing.T) {
	ghErr := errors.New(`gh api --method PATCH gists/abc123 --input -: gh: Not Found (HTTP 404): exit status 1`)

	runner := &fakeRunner{err: ghErr}
	client := New(runner)

	_, err := client.Update(context.Background(), "abc123", "notes.md", "desc", "body")
	if err == nil {
		t.Fatal("Update() error = nil, want a NotFoundError")
	}

	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Update() error = %v, want a NotFoundError", err)
	}
	if notFound.ID != "abc123" {
		t.Errorf("NotFoundError.ID = %q, want abc123", notFound.ID)
	}
}

func TestOtherErrorKeepsStderr(t *testing.T) {
	runner := &fakeRunner{err: errors.New(`gh api --method POST gists --input -: gh: Server Error (HTTP 500): exit status 1`)}
	client := New(runner)

	_, _, err := client.Create(context.Background(), "notes.md", "", "body")
	if err == nil {
		t.Fatal("Create() error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("Create() error = %v, want it to keep the stderr text", err)
	}

	var notFound *NotFoundError
	if errors.As(err, &notFound) {
		t.Errorf("Create() error = %v, want a plain error", err)
	}
}
