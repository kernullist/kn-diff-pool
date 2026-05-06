package paths

import (
	"os"
	"path/filepath"
	"strings"
)

var executablePath = os.Executable

func DefaultDriverPath() string {
	if value := strings.TrimSpace(os.Getenv("KN_DIFF_DRIVER_PATH")); value != "" {
		return Resolve(value)
	}

	if exe, err := executablePath(); err == nil && strings.TrimSpace(exe) != "" {
		return Resolve(filepath.Join(filepath.Dir(exe), "kn-diff.sys"))
	}

	return Resolve("kn-diff.sys")
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func Resolve(path string) string {
	path = strings.Trim(strings.TrimSpace(path), "\"")
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

func Exists(path string) bool {
	return exists(path)
}
