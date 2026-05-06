package exporter

import (
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kn-diff-pool/kn-pool-diff/internal/protocol"
)

var executablePath = os.Executable

type Analysis struct {
	CreatedAt      time.Time               `json:"created_at"`
	Scope          string                  `json:"scope"`
	DiffEntries    []protocol.Entry        `json:"diff_entries"`
	CurrentEntries []protocol.Entry        `json:"current_entries"`
	SearchResults  []protocol.SearchResult `json:"search_results"`
	SearchTarget   string                  `json:"search_target"`
	SearchText     string                  `json:"search_text"`
	SearchKind     string                  `json:"search_kind"`
	TableView      string                  `json:"table_view"`
	HashMode       string                  `json:"hash_mode"`
}

func ExportAnalysis(dir string, format string, diff []protocol.Entry, current []protocol.Entry, search []protocol.SearchResult, searchTarget, searchKind, searchText, tableView, hashMode string) (string, error) {
	dir, err := ensureExportDir(dir)
	if err != nil {
		return "", err
	}

	stamp := time.Now().Format("20060102_150405")
	switch format {
	case "json":
		path := filepath.Join(dir, "knpool_analysis_"+stamp+".json")
		payload := Analysis{
			CreatedAt:      time.Now(),
			Scope:          "big_pool",
			DiffEntries:    diff,
			CurrentEntries: current,
			SearchResults:  search,
			SearchTarget:   searchTarget,
			SearchText:     searchText,
			SearchKind:     searchKind,
			TableView:      tableView,
			HashMode:       hashMode,
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			return "", err
		}
		return path, nil
	case "csv":
		path := filepath.Join(dir, "knpool_analysis_"+stamp+".csv")
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			return "", err
		}
		defer file.Close()

		writer := csv.NewWriter(file)

		if err := writer.Write([]string{
			"scope", "type", "address", "offset", "size", "tag", "flags", "pages",
			"accessible_pages", "writable_pages", "executable_pages", "content_hash",
			"hashed_bytes", "snapshot_id",
		}); err != nil {
			return "", err
		}
		for _, entry := range diff {
			if err := writer.Write(entryRecord("diff", entry, 0)); err != nil {
				return "", err
			}
		}
		for _, entry := range current {
			if err := writer.Write(entryRecord("current", entry, 0)); err != nil {
				return "", err
			}
		}
		for _, result := range search {
			entry := protocol.Entry{
				Address:         result.Address,
				Size:            result.PoolSize,
				Tag:             result.Tag,
				Flags:           result.Flags,
				PageCount:       result.PageCount,
				AccessiblePages: result.AccessiblePages,
				WritablePages:   result.WritablePages,
				ExecutablePages: result.ExecutablePages,
				ContentHash:     result.ContentHash,
				HashedBytes:     result.HashedBytes,
				SnapshotID:      result.SnapshotID,
			}
			if err := writer.Write(entryRecord("search", entry, result.Offset)); err != nil {
				return "", err
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return "", err
		}
		return path, nil
	default:
		return "", fmt.Errorf("unsupported export format %q", format)
	}
}

func SaveDump(dir string, entry protocol.Entry, offset uint64, data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("no dump data to save")
	}

	dir, err := ensureExportDir(dir)
	if err != nil {
		return "", err
	}

	stamp := time.Now().Format("20060102_150405")
	base := fmt.Sprintf("knpool_dump_%016X_%X_%s", entry.Address, offset, stamp)
	binPath := filepath.Join(dir, base+".bin")
	if err := os.WriteFile(binPath, data, 0600); err != nil {
		return "", err
	}

	metaPath := filepath.Join(dir, base+".json")
	meta := struct {
		CreatedAt time.Time      `json:"created_at"`
		Entry     protocol.Entry `json:"entry"`
		Offset    uint64         `json:"offset"`
		Length    int            `json:"length"`
		Hex       string         `json:"hex"`
	}{
		CreatedAt: time.Now(),
		Entry:     entry,
		Offset:    offset,
		Length:    len(data),
		Hex:       hex.EncodeToString(data),
	}
	metaData, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(metaPath, metaData, 0600); err != nil {
		return "", err
	}

	return binPath, nil
}

func entryRecord(kind string, entry protocol.Entry, offset uint64) []string {
	return []string{
		"big_pool",
		kind,
		fmt.Sprintf("0x%016X", entry.Address),
		fmt.Sprintf("0x%X", offset),
		fmt.Sprintf("%d", entry.Size),
		entry.TagString(),
		fmt.Sprintf("0x%08X", entry.Flags),
		fmt.Sprintf("%d", entry.PageCount),
		fmt.Sprintf("%d", entry.AccessiblePages),
		fmt.Sprintf("%d", entry.WritablePages),
		fmt.Sprintf("%d", entry.ExecutablePages),
		fmt.Sprintf("0x%016X", entry.ContentHash),
		fmt.Sprintf("%d", entry.HashedBytes),
		fmt.Sprintf("%d", entry.SnapshotID),
	}
}

func DefaultDir() string {
	if exe, err := executablePath(); err == nil && strings.TrimSpace(exe) != "" {
		return filepath.Join(filepath.Dir(exe), "exports")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "exports"
	}

	return filepath.Join(cwd, "exports")
}

func ensureExportDir(dir string) (string, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	dir = absolute
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}
