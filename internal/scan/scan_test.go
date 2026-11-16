package scan

import (
	"strings"
	"testing"
)

func TestSuspicious(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  []string
	}{
		{name: "dot env", files: []string{".env"}, want: []string{".env"}},
		{name: "dot env variant", files: []string{"config/.env.production"}, want: []string{"config/.env.production"}},
		{name: "dot env example", files: []string{"app/.env.example"}},
		{name: "dot env sample", files: []string{".env.sample"}},
		{name: "pem", files: []string{"server.pem"}, want: []string{"server.pem"}},
		{name: "key", files: []string{"tls/app.key"}, want: []string{"tls/app.key"}},
		{name: "p12", files: []string{"cert.p12"}, want: []string{"cert.p12"}},
		{name: "pfx", files: []string{"cert.pfx"}, want: []string{"cert.pfx"}},
		{name: "keystore", files: []string{"release.keystore"}, want: []string{"release.keystore"}},
		{name: "id_rsa", files: []string{"home/id_rsa"}, want: []string{"home/id_rsa"}},
		{name: "id_dsa", files: []string{"id_dsa"}, want: []string{"id_dsa"}},
		{name: "id_ecdsa", files: []string{"id_ecdsa"}, want: []string{"id_ecdsa"}},
		{name: "id_ed25519", files: []string{"keys/id_ed25519"}, want: []string{"keys/id_ed25519"}},
		{name: "npmrc", files: []string{".npmrc"}, want: []string{".npmrc"}},
		{name: "pypirc", files: []string{".pypirc"}, want: []string{".pypirc"}},
		{name: "netrc", files: []string{"home/.netrc"}, want: []string{"home/.netrc"}},
		{name: "credentials json", files: []string{"credentials.json"}, want: []string{"credentials.json"}},
		{name: "secrets", files: []string{"secrets.yaml"}, want: []string{"secrets.yaml"}},
		{name: "mixed selection", files: []string{"a.js", ".env.local", "b.md", "app.pem"}, want: []string{".env.local", "app.pem"}},
		{name: "plain names", files: []string{"secrets", ".envrc", "README.md", "key.txt"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Suspicious(tt.files)
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("Suspicious() = %v, want %v", got, tt.want)
			}
		})
	}
}
