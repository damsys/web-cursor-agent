package cursor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureStatusIndicatorsCreatesAndUpdates(t *testing.T) {
	dir := t.TempDir()
	if err := EnsureStatusIndicators(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, cliConfigName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	display := root["display"].(map[string]any)
	if display["showStatusIndicators"] != true {
		t.Fatalf("created config = %#v", root)
	}

	existing := []byte(`{
  "version": 1,
  "display": {
    "mode": "zen",
    "showStatusIndicators": false
  },
  "model": "keep-me"
}
`)
	if err := os.WriteFile(path, existing, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureStatusIndicators(dir); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	display = root["display"].(map[string]any)
	if display["showStatusIndicators"] != true {
		t.Fatalf("updated indicators = %#v", display)
	}
	if display["mode"] != "zen" {
		t.Fatalf("mode should be kept: %#v", display)
	}
	if root["model"] != "keep-me" {
		t.Fatalf("other keys should be kept: %#v", root)
	}
}

func TestLayoutEnsureEnablesStatusIndicators(t *testing.T) {
	state := t.TempDir()
	layout, err := LayoutFor(state, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(layout.ConfigDir, cliConfigName))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	display := root["display"].(map[string]any)
	if display["showStatusIndicators"] != true {
		t.Fatalf("layout ensure config = %#v", root)
	}
}
