package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type State struct {
	DriverPath       string `json:"driver_path,omitempty"`
	ExportDir        string `json:"export_dir,omitempty"`
	SearchTarget     uint32 `json:"search_target,omitempty"`
	FilterTag        string `json:"filter_tag,omitempty"`
	FilterWritable   int    `json:"filter_writable,omitempty"`
	FilterExecutable int    `json:"filter_executable,omitempty"`
	FilterAccessible int    `json:"filter_accessible,omitempty"`
	SortMode         int    `json:"sort_mode,omitempty"`
	SortSize         int    `json:"sort_size,omitempty"`
	TableView        string `json:"table_view,omitempty"`
	HashMode         uint32 `json:"hash_mode,omitempty"`
	HashSampleBytes  uint64 `json:"hash_sample_bytes,omitempty"`
}

func Load() (State, string, error) {
	path, err := Path()
	if err != nil {
		return State{}, "", err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, path, nil
	}
	if err != nil {
		return State{}, path, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, path, err
	}

	return state, path, nil
}

func Save(path string, state State) error {
	if path == "" {
		var err error
		path, err = Path()
		if err != nil {
			return err
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

func Path() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "kn-diff-pool", "config.json"), nil
}
