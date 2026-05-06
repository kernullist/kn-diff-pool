package paths

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestDefaultDriverPathUsesExecutableDirectory(t *testing.T) {
	t.Setenv("KN_DIFF_DRIVER_PATH", "")
	exe := filepath.Join(t.TempDir(), "kn-pool-diff.exe")
	defer setExecutablePathForTest(func() (string, error) {
		return exe, nil
	})()

	want := filepath.Join(filepath.Dir(exe), "kn-diff.sys")
	if got := DefaultDriverPath(); got != want {
		t.Fatalf("default driver path mismatch: got %q, want %q", got, want)
	}
}

func TestDefaultDriverPathAllowsEnvironmentOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "override.sys")
	t.Setenv("KN_DIFF_DRIVER_PATH", want)

	if got := DefaultDriverPath(); got != want {
		t.Fatalf("environment driver path mismatch: got %q, want %q", got, want)
	}
}

func TestDefaultDriverPathFallsBackToCurrentDirectory(t *testing.T) {
	t.Setenv("KN_DIFF_DRIVER_PATH", "")
	defer setExecutablePathForTest(func() (string, error) {
		return "", errors.New("no executable")
	})()

	want := Resolve("kn-diff.sys")
	if got := DefaultDriverPath(); got != want {
		t.Fatalf("fallback driver path mismatch: got %q, want %q", got, want)
	}
}

func setExecutablePathForTest(fn func() (string, error)) func() {
	previous := executablePath
	executablePath = fn
	return func() {
		executablePath = previous
	}
}
