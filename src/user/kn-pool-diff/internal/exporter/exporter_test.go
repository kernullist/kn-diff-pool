package exporter

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kn-diff-pool/kn-pool-diff/internal/protocol"
)

func TestExportAnalysisUsesCustomDirectory(t *testing.T) {
	dir := t.TempDir()
	diff := []protocol.Entry{{Address: 0x1000, Size: 0x80, Tag: protocol.PoolTag("DIFF")}}
	current := []protocol.Entry{{Address: 0x2000, Size: 0x100, Tag: protocol.PoolTag("CURR")}}
	search := []protocol.SearchResult{{Address: 0x1000, Offset: 4, PoolSize: 0x80, Tag: protocol.PoolTag("DIFF")}}

	path, err := ExportAnalysis(dir, "json", diff, current, search, "diff", "ascii", "needle", "current", "sample")
	if err != nil {
		t.Fatalf("ExportAnalysis returned error: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("export dir mismatch: got %q, want %q", filepath.Dir(path), dir)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	var analysis Analysis
	if err := json.Unmarshal(data, &analysis); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if analysis.Scope != "big_pool" || len(analysis.DiffEntries) != 1 || len(analysis.CurrentEntries) != 1 || len(analysis.SearchResults) != 1 {
		t.Fatalf("analysis contents mismatch: %#v", analysis)
	}
}

func TestExportAnalysisCSVIncludesCurrentRows(t *testing.T) {
	dir := t.TempDir()
	path, err := ExportAnalysis(
		dir,
		"csv",
		[]protocol.Entry{{Address: 0x1000, Size: 0x80, Tag: protocol.PoolTag("DIFF")}},
		[]protocol.Entry{{Address: 0x2000, Size: 0x100, Tag: protocol.PoolTag("CURR")}},
		nil,
		"diff",
		"",
		"",
		"diff",
		"full")
	if err != nil {
		t.Fatalf("ExportAnalysis returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "big_pool,diff,") || !strings.Contains(text, "big_pool,current,") {
		t.Fatalf("csv rows missing expected types: %s", text)
	}
}

func TestSaveDumpWritesBinaryAndMetadata(t *testing.T) {
	dir := t.TempDir()
	path, err := SaveDump(dir, protocol.Entry{Address: 0x1000, Size: 0x80, Tag: protocol.PoolTag("DUMP")}, 0x10, []byte{1, 2, 3})
	if err != nil {
		t.Fatalf("SaveDump returned error: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("dump dir mismatch: got %q, want %q", filepath.Dir(path), dir)
	}
	if _, err := os.Stat(strings.TrimSuffix(path, ".bin") + ".json"); err != nil {
		t.Fatalf("dump metadata missing: %v", err)
	}
}

func TestDefaultDirUsesExecutableDirectory(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "kn-pool-diff.exe")
	defer setExecutablePathForTest(func() (string, error) {
		return exe, nil
	})()

	want := filepath.Join(filepath.Dir(exe), "exports")
	if got := DefaultDir(); got != want {
		t.Fatalf("default export dir mismatch: got %q, want %q", got, want)
	}
}

func TestDefaultDirFallsBackToCurrentDirectory(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd returned error: %v", err)
	}
	defer setExecutablePathForTest(func() (string, error) {
		return "", errors.New("no executable")
	})()

	want := filepath.Join(cwd, "exports")
	if got := DefaultDir(); got != want {
		t.Fatalf("fallback export dir mismatch: got %q, want %q", got, want)
	}
}

func setExecutablePathForTest(fn func() (string, error)) func() {
	previous := executablePath
	executablePath = fn
	return func() {
		executablePath = previous
	}
}
