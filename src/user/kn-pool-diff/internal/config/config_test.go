package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveWritesParentDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	state := State{
		DriverPath:      `C:\drivers\kn-diff.sys`,
		ExportDir:       `C:\exports`,
		SearchTarget:    1,
		TableView:       "current",
		HashSampleBytes: 0x10000,
	}

	if err := Save(path, state); err != nil {
		t.Fatalf("Save returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	var loaded State
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if loaded.DriverPath != state.DriverPath || loaded.ExportDir != state.ExportDir || loaded.TableView != state.TableView {
		t.Fatalf("loaded config mismatch: got %#v, want %#v", loaded, state)
	}
}
