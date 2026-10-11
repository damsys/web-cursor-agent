package main

import (
	"path/filepath"
	"testing"
)

func TestSuggestConfigPathRelativeToConfigDir(t *testing.T) {
	configDirectory := filepath.Join(string(filepath.Separator), "repo")
	cert := filepath.Join(configDirectory, "var", "tls", "server.crt")
	got := suggestConfigPath(configDirectory, cert)
	if got != "var/tls/server.crt" {
		t.Fatalf("got %q", got)
	}
}

func TestSuggestConfigPathKeepsOutsideAbsolute(t *testing.T) {
	configDirectory := filepath.Join(string(filepath.Separator), "repo")
	cert := filepath.Join(string(filepath.Separator), "elsewhere", "server.crt")
	got := suggestConfigPath(configDirectory, cert)
	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path, got %q", got)
	}
}
