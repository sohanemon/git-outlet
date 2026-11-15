package selection

import (
	"fmt"
	"path"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Unmatched holds patterns that matched no file.
type Unmatched struct {
	Include []string
	Exclude []string
}

// Filter selects files by include and exclude patterns; exclude always wins.
func Filter(files []string, include, exclude []string) ([]string, Unmatched, error) {
	if err := validate(include, "include"); err != nil {
		return nil, Unmatched{}, err
	}
	if err := validate(exclude, "exclude"); err != nil {
		return nil, Unmatched{}, err
	}

	selected := make([]string, 0, len(files))
	if len(include) == 0 {
		selected = append(selected, files...)
	} else {
		for _, file := range files {
			if matchesAny(include, file) {
				selected = append(selected, file)
			}
		}
	}

	kept := selected[:0]
	for _, file := range selected {
		if !matchesAny(exclude, file) {
			kept = append(kept, file)
		}
	}

	var unmatched Unmatched
	for _, pattern := range include {
		if !matchesAnyOf([]string{pattern}, files) {
			unmatched.Include = append(unmatched.Include, pattern)
		}
	}
	for _, pattern := range exclude {
		if !matchesAnyOf([]string{pattern}, files) {
			unmatched.Exclude = append(unmatched.Exclude, pattern)
		}
	}

	return kept, unmatched, nil
}

func validate(patterns []string, field string) error {
	for _, pattern := range patterns {
		if !doublestar.ValidatePattern(pattern) {
			return fmt.Errorf("invalid %s pattern %q", field, pattern)
		}
	}

	return nil
}

func matchesAny(patterns []string, file string) bool {
	for _, pattern := range patterns {
		if matches(pattern, file) {
			return true
		}
	}

	return false
}

func matchesAnyOf(patterns, files []string) bool {
	for _, file := range files {
		if matchesAny(patterns, file) {
			return true
		}
	}

	return false
}

func matches(pattern, file string) bool {
	target := file
	// INFO: a pattern without a slash matches the base name at any depth, like gitignore.
	if !strings.Contains(pattern, "/") {
		target = path.Base(file)
	}

	ok, err := doublestar.Match(pattern, target)

	return err == nil && ok
}
