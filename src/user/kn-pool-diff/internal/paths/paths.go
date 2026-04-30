package paths

import (
	"os"
	"path/filepath"
	"strings"
)

func DefaultDriverPath() string {
	if value := os.Getenv("KN_DIFF_DRIVER_PATH"); value != "" {
		return Resolve(value)
	}

	starts := []string{}
	if cwd, err := os.Getwd(); err == nil {
		starts = append(starts, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		starts = append(starts, filepath.Dir(exe))
	}

	for _, start := range starts {
		if found, ok := findUp(start); ok {
			return found
		}
	}

	if len(starts) > 0 {
		return Resolve(filepath.Join(starts[0], "src", "kn-diff", "x64", "Debug", "kn-diff.sys"))
	}
	return "kn-diff.sys"
}

func findUp(start string) (string, bool) {
	dir := absolute(start)
	for {
		candidates := []string{
			filepath.Join(dir, "src", "kn-diff", "x64", "Debug", "kn-diff.sys"),
			filepath.Join(dir, "kn-diff.sys"),
		}

		for _, candidate := range candidates {
			if exists(candidate) {
				return absolute(candidate), true
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
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

func absolute(path string) string {
	return Resolve(path)
}
