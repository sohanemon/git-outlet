package scan

import (
	"path/filepath"
	"strings"
)

// NOTE: Return likely secret filenames so callers can warn or block them.
func Suspicious(files []string) []string {
	matches := make([]string, 0, len(files))
	for _, file := range files {
		if suspicious(filepath.Base(file)) {
			matches = append(matches, file)
		}
	}

	return matches
}

var secretNames = map[string]struct{}{
	"id_rsa":           {},
	"id_dsa":           {},
	"id_ecdsa":         {},
	"id_ed25519":       {},
	".npmrc":           {},
	".pypirc":          {},
	".netrc":           {},
	"credentials.json": {},
}

// INFO: Exclude example and sample files because they are publishable templates.
func suspicious(base string) bool {
	if base == ".env" {
		return true
	}
	if strings.HasPrefix(base, ".env.") && base != ".env.example" && base != ".env.sample" {
		return true
	}
	if _, ok := secretNames[base]; ok {
		return true
	}
	if strings.HasPrefix(base, "secrets.") && len(base) > len("secrets.") {
		return true
	}

	switch strings.ToLower(filepath.Ext(base)) {
	case ".pem", ".key", ".p12", ".pfx", ".keystore":
		return true
	}

	return false
}
