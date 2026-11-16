package repo

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

func TestSubtreeSplitPushAndReject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := newSourceRepo(t, map[string]string{"project/a.txt": "one", "root.txt": "root"})
	runner := &fakeRunner{}
	client := New(runner)
	ctx := context.Background()

	first, err := client.SubtreeSplit(ctx, root, "project")
	if err != nil {
		if strings.Contains(err.Error(), "git subtree is not available") {
			t.Skip("git-subtree is not installed")
		}
		t.Fatalf("SubtreeSplit() error: %v", err)
	}
	second, err := client.SubtreeSplit(ctx, root, "project")
	if err != nil {
		t.Fatalf("second SubtreeSplit() error: %v", err)
	}
	if first != second {
		t.Fatalf("split SHA changed from %s to %s without commits", first, second)
	}
	if got := runGit(t, root, "remote"); strings.TrimSpace(got) != "" {
		t.Fatalf("private repo remotes = %q, want none", got)
	}

	bare := newBareRepo(t)
	if err := client.SubtreePush(ctx, SubtreeOptions{Root: root, URL: bare, Branch: "main", Commit: first}); err != nil {
		t.Fatalf("SubtreePush() error: %v", err)
	}
	if got := strings.TrimSpace(runGit(t, bare, "rev-parse", "refs/heads/main")); got != first {
		t.Errorf("remote SHA = %s, want %s", got, first)
	}

	writeFile(t, root, "project/a.txt", "two")
	runGit(t, root, "add", "project/a.txt")
	runGit(t, root, "commit", "-q", "-m", "update project")
	updated, err := client.SubtreeSplit(ctx, root, "project")
	if err != nil {
		t.Fatalf("updated SubtreeSplit() error: %v", err)
	}
	if updated == first {
		t.Fatal("split SHA did not change after a path commit")
	}
	if err := client.SubtreePush(ctx, SubtreeOptions{Root: root, URL: bare, Branch: "main", Commit: updated}); err != nil {
		t.Fatalf("updated SubtreePush() error: %v", err)
	}

	other := newBareRepo(t)
	seedRemote(t, other, map[string]string{"unrelated.txt": "outside"})
	if err := client.SubtreePush(ctx, SubtreeOptions{Root: root, URL: other, Branch: "main", Commit: updated}); err == nil {
		t.Fatal("SubtreePush() error = nil, want rejected non-fast-forward")
	} else if !strings.Contains(err.Error(), "changed outside git-outlet") {
		t.Errorf("SubtreePush() error = %v, want outside-change hint", err)
	}
	for _, call := range runner.gitCalls {
		if strings.Contains(call, "--force") || strings.Contains(call, "-f ") {
			t.Errorf("SubtreePush() force-pushed: %s", call)
		}
	}
}

func TestSubtreeSplitWithoutPathHistory(t *testing.T) {
	root := newSourceRepo(t, map[string]string{"root.txt": "root"})
	writeFile(t, root, "new-folder/file.txt", "uncommitted")
	_, err := New(&fakeRunner{}).SubtreeSplit(context.Background(), root, "new-folder")
	if err == nil || !strings.Contains(err.Error(), "no committed history yet") {
		t.Fatalf("SubtreeSplit() error = %v, want no-history explanation", err)
	}
}
