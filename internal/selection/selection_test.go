package selection

import (
	"reflect"
	"strings"
	"testing"
)

var sample = []string{
	"a.js",
	"README.md",
	"src/b.js",
	"src/deep/c.test.js",
	"fixtures/x.json",
}

func TestFilter(t *testing.T) {
	tests := []struct {
		name    string
		include []string
		exclude []string
		want    []string
		wantUn  Unmatched
	}{
		{
			name:   "no patterns selects everything",
			want:   sample,
			wantUn: Unmatched{},
		},
		{
			name:    "include only",
			include: []string{"src/**"},
			want:    []string{"src/b.js", "src/deep/c.test.js"},
		},
		{
			name:    "exclude only",
			exclude: []string{"*.test.js"},
			want:    []string{"a.js", "README.md", "src/b.js", "fixtures/x.json"},
		},
		{
			name:    "include and exclude",
			include: []string{"src/**"},
			exclude: []string{"src/deep/**"},
			want:    []string{"src/b.js"},
		},
		{
			name:    "exclude wins",
			include: []string{"a.js"},
			exclude: []string{"a.js"},
			want:    []string{},
		},
		{
			name:    "bare pattern matches base at any depth",
			exclude: []string{"b.js"},
			want:    []string{"a.js", "README.md", "src/deep/c.test.js", "fixtures/x.json"},
		},
		{
			name:    "pattern with slash matches full path",
			include: []string{"fixtures/**"},
			want:    []string{"fixtures/x.json"},
		},
		{
			name:    "unmatched patterns reported",
			include: []string{"src/**", "docs/**"},
			exclude: []string{"*.md"},
			want:    []string{"src/b.js", "src/deep/c.test.js"},
			wantUn:  Unmatched{Include: []string{"docs/**"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, unmatched, err := Filter(sample, tt.include, tt.exclude)
			if err != nil {
				t.Fatalf("Filter() error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Filter() = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(unmatched, tt.wantUn) {
				t.Errorf("Filter() unmatched = %+v, want %+v", unmatched, tt.wantUn)
			}
		})
	}
}

func TestFilterInvalidPattern(t *testing.T) {
	tests := []struct {
		name    string
		include []string
		exclude []string
		want    string
	}{
		{name: "invalid include", include: []string{"[bad"}, want: "[bad"},
		{name: "invalid exclude", exclude: []string{"src/[bad"}, want: "src/[bad"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := Filter(sample, tt.include, tt.exclude)
			if err == nil {
				t.Fatal("Filter() error = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Filter() error = %v, want it to name %q", err, tt.want)
			}
		})
	}
}
