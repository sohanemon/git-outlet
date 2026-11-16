package config_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sohanemon/git-outlet/internal/check"
	"github.com/sohanemon/git-outlet/internal/config"
)

func TestExampleConfigPassesStructuralChecks(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(filepath.Join(root, "outlet.example.yml"))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	report, err := check.Run(context.Background(), cfg, check.Options{Root: root})
	if err != nil {
		t.Fatalf("check.Run() error: %v", err)
	}
	if report.Errors() != 0 {
		t.Fatalf("example config has structural errors: %+v", report.Problems)
	}
}
