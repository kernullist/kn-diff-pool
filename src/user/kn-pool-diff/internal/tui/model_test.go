package tui

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/sys/windows/svc"

	"kn-diff-pool/kn-pool-diff/internal/device"
	"kn-diff-pool/kn-pool-diff/internal/privilege"
	"kn-diff-pool/kn-pool-diff/internal/protocol"
	"kn-diff-pool/kn-pool-diff/internal/scm"
)

func TestVisibleDiffFiltersAndSorts(t *testing.T) {
	model := newTestModel()
	model.diff = []protocol.Entry{
		{Address: 0x1000, Size: 0x100, Tag: protocol.PoolTag("ABCD"), PageCount: 1, AccessiblePages: 1, WritablePages: 0, ExecutablePages: 0},
		{Address: 0x2000, Size: 0x400, Tag: protocol.PoolTag("WXYZ"), PageCount: 4, AccessiblePages: 2, WritablePages: 2, ExecutablePages: 0},
		{Address: 0x3000, Size: 0x200, Tag: protocol.PoolTag("EXEC"), PageCount: 2, AccessiblePages: 1, WritablePages: 1, ExecutablePages: 1},
	}

	model.filterWritable = triYes
	model.sortMode = sortPagesDesc

	got := model.visibleDiff()
	addresses := []uint64{}
	for _, entry := range got {
		addresses = append(addresses, entry.Address)
	}

	want := []uint64{0x2000, 0x3000}
	if !reflect.DeepEqual(addresses, want) {
		t.Fatalf("visible addresses mismatch: got %#v, want %#v", addresses, want)
	}
}

func TestVisibleDiffDefaultsToAddressSort(t *testing.T) {
	model := newTestModel()
	model.diff = []protocol.Entry{
		{Address: 0x3000, Size: 0x3000, PageCount: 3},
		{Address: 0x1000, Size: 0x1000, PageCount: 1},
		{Address: 0x2000, Size: 0x2000, PageCount: 2},
	}

	got := model.visibleDiff()
	addresses := []uint64{}
	for _, entry := range got {
		addresses = append(addresses, entry.Address)
	}

	want := []uint64{0x1000, 0x2000, 0x3000}
	if !reflect.DeepEqual(addresses, want) {
		t.Fatalf("default address sort mismatch: got %#v, want %#v", addresses, want)
	}
}

func TestSortKeyUsesLowercaseO(t *testing.T) {
	model := startedTestModel()
	model.diffReady = true
	model.diff = []protocol.Entry{{Address: 0x1000, Size: 0x1000, PageCount: 1}}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd != nil {
		t.Fatalf("sort should not return a command")
	}
	got := updated.(Model)
	if got.sortMode != sortPagesDesc {
		t.Fatalf("sort mode mismatch: got %d", got.sortMode)
	}
	if !containsLog(got.logs, "sort: pages desc") {
		t.Fatalf("sort log missing: %#v", got.logs)
	}
}

func TestTagFilterIsCaseInsensitiveAndPartial(t *testing.T) {
	model := newTestModel()
	model.diff = []protocol.Entry{
		{Address: 0x1000, Tag: protocol.PoolTag("KDTP")},
		{Address: 0x2000, Tag: protocol.PoolTag("ABCD")},
	}
	model.filterTag = "dt"

	got := model.visibleDiff()
	if len(got) != 1 || got[0].Address != 0x1000 {
		t.Fatalf("tag filter mismatch: got %#v", got)
	}
}

func TestVisibleSearchUsesSameFiltersAndSort(t *testing.T) {
	model := newTestModel()
	model.search = []protocol.SearchResult{
		{Address: 0x1000, PoolSize: 0x100, Tag: protocol.PoolTag("ABCD"), PageCount: 1, WritablePages: 0},
		{Address: 0x2000, PoolSize: 0x400, Tag: protocol.PoolTag("WXYZ"), PageCount: 4, WritablePages: 2},
		{Address: 0x3000, PoolSize: 0x200, Tag: protocol.PoolTag("EXEC"), PageCount: 2, WritablePages: 1},
	}
	model.filterWritable = triYes
	model.sortMode = sortPagesDesc

	got := model.visibleSearch()
	addresses := []uint64{}
	for _, result := range got {
		addresses = append(addresses, result.Address)
	}

	want := []uint64{0x2000, 0x3000}
	if !reflect.DeepEqual(addresses, want) {
		t.Fatalf("visible search mismatch: got %#v, want %#v", addresses, want)
	}
}

func TestParseSearchPatternUTF16LE(t *testing.T) {
	got, err := parseSearchPattern("utf16", "AZ")
	if err != nil {
		t.Fatalf("parseSearchPattern returned error: %v", err)
	}

	want := []byte{'A', 0, 'Z', 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("utf16 pattern mismatch: got %#v, want %#v", got, want)
	}
}

func TestSearchTargetToggle(t *testing.T) {
	if got := toggleSearchTarget(protocol.SearchTargetDiff); got != protocol.SearchTargetCurrent {
		t.Fatalf("diff toggle mismatch: got %d", got)
	}
	if got := toggleSearchTarget(protocol.SearchTargetCurrent); got != protocol.SearchTargetDiff {
		t.Fatalf("current toggle mismatch: got %d", got)
	}
}

func TestExportSearchTargetUsesLastSearchWhenAvailable(t *testing.T) {
	model := newTestModel()
	model.searchTarget = protocol.SearchTargetCurrent
	if got := model.exportSearchTarget(); got != "current" {
		t.Fatalf("empty search export target mismatch: got %q", got)
	}

	model.searchText = "needle"
	model.searchStats.Target = protocol.SearchTargetDiff
	if got := model.exportSearchTarget(); got != "diff" {
		t.Fatalf("searched export target mismatch: got %q", got)
	}
}

func TestVisibleTableCanUseCurrentSnapshot(t *testing.T) {
	model := newTestModel()
	model.diff = []protocol.Entry{{Address: 0x1000, Size: 0x100, Tag: protocol.PoolTag("DIFF")}}
	model.currentEntries = []protocol.Entry{
		{Address: 0x2000, Size: 0x400, Tag: protocol.PoolTag("CURR")},
		{Address: 0x3000, Size: 0x200, Tag: protocol.PoolTag("SKIP")},
	}
	model.tableView = tableViewCurrent
	model.filterTag = "cur"

	got := model.visibleTable()
	if len(got) != 1 || got[0].Address != 0x2000 {
		t.Fatalf("current table mismatch: got %#v", got)
	}
}

func TestTableToggle(t *testing.T) {
	if got := toggleTableView(tableViewDiff); got != tableViewCurrent {
		t.Fatalf("table toggle mismatch: got %q", got)
	}
	if got := toggleTableView(tableViewCurrent); got != tableViewDiff {
		t.Fatalf("table toggle back mismatch: got %q", got)
	}
}

func TestSwitchingToCurrentTableLoadsEntriesEvenWhenCountUnknown(t *testing.T) {
	model := startedTestModel()
	model.configPath = filepath.Join(t.TempDir(), "config.json")
	model.diffReady = true

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyTab})
	if cmd == nil {
		t.Fatalf("expected current table switch to request current entries")
	}
	got := updated.(Model)
	if got.tableView != tableViewCurrent {
		t.Fatalf("table view mismatch: got %q", got.tableView)
	}
}

func TestStatusDoesNotOverwriteCommandLogUnlessRefreshing(t *testing.T) {
	model := newTestModel()
	model.addLog("start requested")
	updated, _ := model.Update(statusMsg{status: scm.DriverStatus{Installed: false, StateText: "not installed"}})
	model = updated.(Model)
	if !containsLog(model.logs, "start requested") {
		t.Fatalf("internal status update should not overwrite command log: %#v", model.logs)
	}

	model.busy = "refreshing"
	updated, _ = model.Update(statusMsg{status: scm.DriverStatus{Installed: true, State: svc.Stopped, StateText: "stopped"}})
	model = updated.(Model)
	if !containsLog(model.logs, "refresh ok: installed, stopped") {
		t.Fatalf("refresh status log missing: %#v", model.logs)
	}
}

func TestLogShowsOnlyLastResult(t *testing.T) {
	model := newTestModel()
	model.addLog("first")
	model.addLog("second")

	if got := model.visibleLogs(); len(got) != 1 || got[0] != "second" {
		t.Fatalf("visible logs mismatch: %#v", got)
	}
}

func TestStartKeyLogsBeforeCommandRuns(t *testing.T) {
	driverPath := filepath.Join(t.TempDir(), "kn-diff.sys")
	if err := os.WriteFile(driverPath, []byte("test"), 0600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	model := newTestModel()
	model.admin = privilege.Status{Elevated: true}
	model.driverPath = driverPath

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd == nil {
		t.Fatalf("expected start command")
	}
	got := updated.(Model)
	if got.busy != "installing and loading" {
		t.Fatalf("busy mismatch: got %q", got.busy)
	}
	if !containsLog(got.logs, "start requested: installing service and loading driver") {
		t.Fatalf("start progress log missing: %#v", got.logs)
	}
}

func TestStartKeyIsIgnoredWhenDriverAlreadyRunning(t *testing.T) {
	model := startedTestModel()
	model.addLog("diff loaded: 103/103 entries")

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	if cmd != nil {
		t.Fatalf("start key should not run while driver is already running")
	}
	got := updated.(Model)
	if got.busy != "" {
		t.Fatalf("busy should remain empty: got %q", got.busy)
	}
	if !containsLog(got.logs, "diff loaded: 103/103 entries") {
		t.Fatalf("start key should not overwrite current log: %#v", got.logs)
	}
}

func TestBaselineRefreshResetsDependentSnapshots(t *testing.T) {
	model := startedTestModel()
	model.baseline = 10
	model.baselineReady = true
	model.currentTotal = 20
	model.currentReady = true
	model.currentEntries = []protocol.Entry{{Address: 0x2000}}
	model.diffTotal = 3
	model.diffReady = true
	model.diff = []protocol.Entry{{Address: 0x3000}}
	model.selected = 1
	model.tableView = tableViewCurrent
	model.dumpData = []byte{0x41}
	model.pteDetails = device.PTEDetailsResult{Pages: []protocol.PTEPage{{VirtualAddress: 0x3000}}}
	model.searchText = "needle"
	model.search = []protocol.SearchResult{{Address: 0x3000}}

	updated, cmd := model.Update(snapshotMsg{title: "baseline", count: 42})
	if cmd != nil {
		t.Fatalf("baseline refresh should not return a command")
	}
	got := updated.(Model)
	if got.baseline != 42 || !got.baselineReady {
		t.Fatalf("baseline was not refreshed: count=%d ready=%v", got.baseline, got.baselineReady)
	}
	if got.currentReady || got.currentTotal != 0 || len(got.currentEntries) != 0 {
		t.Fatalf("current snapshot was not reset: ready=%v total=%d entries=%#v", got.currentReady, got.currentTotal, got.currentEntries)
	}
	if got.diffReady || got.diffTotal != 0 || len(got.diff) != 0 {
		t.Fatalf("diff snapshot was not reset: ready=%v total=%d entries=%#v", got.diffReady, got.diffTotal, got.diff)
	}
	if got.selected != 0 || got.tableView != tableViewDiff {
		t.Fatalf("table state mismatch: selected=%d table=%q", got.selected, got.tableView)
	}
	if len(got.dumpData) != 0 || len(got.pteDetails.Pages) != 0 || got.searchText != "" || len(got.search) != 0 {
		t.Fatalf("analysis state was not cleared: dump=%d pte=%d search=%q/%d", len(got.dumpData), len(got.pteDetails.Pages), got.searchText, len(got.search))
	}
	if !containsLog(got.logs, "baseline refreshed: 42 big pools") {
		t.Fatalf("baseline refresh log missing: %#v", got.logs)
	}
}

func TestStopActionMarksDeviceDeleted(t *testing.T) {
	model := newTestModel()
	updated, cmd := model.Update(actionMsg{title: "stop"})
	if cmd == nil {
		t.Fatalf("expected stop action to refresh status")
	}
	got := updated.(Model)
	if got.deviceText != "unavailable (service deleted)" {
		t.Fatalf("device text mismatch: got %q", got.deviceText)
	}
}

func TestShutdownSuccessQuitsAfterDelete(t *testing.T) {
	model := newTestModel()
	updated, cmd := model.Update(shutdownMsg{
		result: scm.UnloadResult{Installed: true, WasRunning: true, Deleted: true, FinalState: "deleted"},
	})
	if cmd == nil {
		t.Fatalf("successful shutdown should quit")
	}
	got := updated.(Model)
	if !containsLog(got.logs, "driver stopped and service deleted; exiting") {
		t.Fatalf("shutdown success log missing: %#v", got.logs)
	}
}

func TestShutdownFailureBlocksQuit(t *testing.T) {
	model := newTestModel()
	updated, cmd := model.Update(shutdownMsg{err: syscall.Errno(5)})
	if cmd != nil {
		t.Fatalf("failed shutdown should not quit")
	}
	got := updated.(Model)
	if !containsLogPrefix(got.logs, "exit blocked: cleanup failed:") {
		t.Fatalf("shutdown failure log missing: %#v", got.logs)
	}
}

func TestQuitDoesNotRequireAdministrator(t *testing.T) {
	model := newTestModel()
	model.admin = privilege.Status{Elevated: false}
	model.status = scm.DriverStatus{Installed: true, State: svc.Running, StateText: "running"}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatalf("non-admin quit should still return a quit command")
	}
	got := updated.(Model)
	if got.busy != "" {
		t.Fatalf("non-admin quit should not enter cleanup busy state: %q", got.busy)
	}
	if !containsLog(got.logs, "exit requested: not elevated, skipping driver unload") {
		t.Fatalf("non-admin quit log missing: %#v", got.logs)
	}
}

func TestViewRendersHelpOnlyOnce(t *testing.T) {
	view := newTestModel().View()
	for _, marker := range []string{"[s]", "[q]"} {
		if got := strings.Count(view, marker); got != 1 {
			t.Fatalf("help marker %q count mismatch: got %d\n%s", marker, got, view)
		}
	}
	for _, marker := range []string{"[i]", "[d]", "[p]", "[t]", "[b]"} {
		if strings.Contains(view, marker) {
			t.Fatalf("unavailable shortcut %q is still visible:\n%s", marker, view)
		}
	}
	if strings.LastIndex(view, "[s]") < strings.LastIndex(view, "Log") {
		t.Fatalf("main shortcuts should be rendered at the bottom:\n%s", view)
	}
	if strings.Contains(view, "Hash:") || strings.Contains(view, "hash policy") {
		t.Fatalf("hash policy should not be visible in the main view:\n%s", view)
	}
}

func TestRenderScreenScrollsBodyAndKeepsFooter(t *testing.T) {
	body := strings.Join([]string{"body0", "body1", "body2", "body3", "body4"}, "\n")
	footer := "Log\nlast\n[s] start"

	view := renderScreen(body, footer, 5, 2)
	lines := strings.Split(view, "\n")
	if len(lines) != 5 {
		t.Fatalf("screen height mismatch: got %d lines: %#v", len(lines), lines)
	}
	if lines[0] != "body2" || lines[1] != "body3" {
		t.Fatalf("body was not scrolled correctly: %#v", lines)
	}
	if lines[2] != "Log" || lines[4] != "[s] start" {
		t.Fatalf("footer was not fixed at bottom: %#v", lines)
	}
}

func TestRenderHelpShowsOnlyServiceCommandsBeforeStart(t *testing.T) {
	help := renderHelp(newTestModel())
	if strings.Contains(help, "[i]") || strings.Contains(help, "[d]") {
		t.Fatalf("removed install/delete shortcuts are still visible:\n%s", help)
	}
	for _, hidden := range []string{"[p]", "[t]", "[b]", "[c]", "[l]", "[tab]", "[enter/x]", "[/]", "[r]", "[v]", "[esc]"} {
		if strings.Contains(help, hidden) {
			t.Fatalf("command %q should be hidden before the driver is started:\n%s", hidden, help)
		}
	}
	for _, visible := range []string{"[s]", "[q]"} {
		if !strings.Contains(help, visible) {
			t.Fatalf("command %q should be visible before the driver is started:\n%s", visible, help)
		}
	}
}

func TestRenderHelpShowsBaselineAfterStart(t *testing.T) {
	model := startedTestModel()

	help := renderHelp(model)
	if !strings.Contains(help, "[b]") {
		t.Fatalf("baseline shortcut should be visible after start:\n%s", help)
	}
	if !strings.Contains(help, "[t]") || strings.Contains(help, "[s]") {
		t.Fatalf("start/stop shortcuts mismatch after start:\n%s", help)
	}
	if strings.Contains(help, "[c]") || strings.Contains(help, "[tab]") {
		t.Fatalf("later snapshot shortcuts should stay hidden before baseline:\n%s", help)
	}
}

func TestRenderHelpShowsCurrentAfterBaseline(t *testing.T) {
	model := startedTestModel()
	model.baselineReady = true

	help := renderHelp(model)
	if !strings.Contains(help, "[c]") {
		t.Fatalf("current shortcut should be visible after baseline:\n%s", help)
	}
	if strings.Contains(help, "[tab]") || strings.Contains(help, "[enter/x]") {
		t.Fatalf("analysis shortcuts should stay hidden before diff is ready:\n%s", help)
	}
}

func TestRenderHelpShowsAnalysisAfterDiff(t *testing.T) {
	model := startedTestModel()
	model.baselineReady = true
	model.currentReady = true
	model.diffReady = true
	model.diff = []protocol.Entry{{Address: 0x1000, Size: 0x1000, PageCount: 1}}

	help := renderHelp(model)
	for _, visible := range []string{"[c]", "[l]", "[tab]", "[up/down]", "[enter/x]", "[p]", "[d]", "[/]", "[f]", "[w/e/a]", "[o]", "[r]"} {
		if !strings.Contains(help, visible) {
			t.Fatalf("command %q should be visible after diff is ready:\n%s", visible, help)
		}
	}
	if strings.Contains(help, "[v]") {
		t.Fatalf("dump save should be hidden until a dump exists:\n%s", help)
	}
}

func TestRenderHelpShowsContextCommandsForDumpAndSearch(t *testing.T) {
	model := startedTestModel()
	model.diffReady = true
	model.diff = []protocol.Entry{{Address: 0x1000, Size: 0x1000, PageCount: 1}}
	model.dumpData = []byte{0x41}
	model.searchText = "A"
	model.search = []protocol.SearchResult{{Address: 0x1000, PoolSize: 0x1000, PageCount: 1}}

	help := renderHelp(model)
	for _, visible := range []string{"[esc]", "[v]", "[g]", "[n]"} {
		if !strings.Contains(help, visible) {
			t.Fatalf("context command %q should be visible:\n%s", visible, help)
		}
	}
}

func TestSelectedPoolDetailExplainsPagesAndFlags(t *testing.T) {
	entry := protocol.Entry{
		Address:         0x1000,
		Size:            0x2000,
		Tag:             protocol.PoolTag("ABCD"),
		Flags:           protocol.FlagNonPaged | protocol.FlagAccessible | protocol.FlagAccessibleAll | protocol.FlagHashSampled,
		PageCount:       2,
		AccessiblePages: 2,
		WritablePages:   1,
		ExecutablePages: 0,
		ContentHash:     0x1234,
		HashedBytes:     0x1000,
	}

	detail := renderPoolDetail(entry)
	for _, want := range []string{
		"accessible 2/2, writable 1/2, executable 0/2",
		"nonpaged",
		"all pages accessible",
		"hash sampled",
		"a/w/x means accessible",
	} {
		if !strings.Contains(detail, want) {
			t.Fatalf("selected detail does not contain %q:\n%s", want, detail)
		}
	}
}

func TestEnterDumpsSelectedPool(t *testing.T) {
	model := startedTestModel()
	model.diffReady = true
	model.diff = []protocol.Entry{{Address: 0x1000, Size: 0x100, Tag: protocol.PoolTag("ABCD")}}

	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatalf("expected enter to dump the selected pool")
	}
}

func TestPoolSaveUsesSelectedCurrentPool(t *testing.T) {
	model := startedTestModel()
	model.diffReady = true
	model.currentReady = true
	model.tableView = tableViewCurrent
	model.currentEntries = []protocol.Entry{
		{Address: 0x1000, Size: 0x100, Tag: protocol.PoolTag("OLD1")},
		{Address: 0x2000, Size: 0x200, Tag: protocol.PoolTag("CURR")},
	}
	model.selected = 1

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if cmd == nil {
		t.Fatalf("expected p to save the selected current pool")
	}
	got := updated.(Model)
	if got.busy != "saving selected pool" {
		t.Fatalf("busy mismatch: got %q", got.busy)
	}
	if !containsLog(got.logs, "pool save requested: 0x0000000000002000") {
		t.Fatalf("pool save log missing: %#v", got.logs)
	}
}

func TestReadPoolDumpChunksAndMarksTruncated(t *testing.T) {
	previous := readContent
	previousMax := maxPoolDumpBytes
	defer func() {
		readContent = previous
		maxPoolDumpBytes = previousMax
	}()

	maxPoolDumpBytes = protocol.MaxReadLength*2 + 2
	var calls []uint64
	readContent = func(address uint64, offset uint64, length uint32) ([]byte, error) {
		if address != 0x2000 {
			t.Fatalf("address mismatch: got 0x%X", address)
		}
		calls = append(calls, offset)
		return make([]byte, length), nil
	}

	entry := protocol.Entry{Address: 0x2000, Size: maxPoolDumpBytes + 1}
	data, requestedLength, truncated, err := readPoolDump(entry)
	if err != nil {
		t.Fatalf("readPoolDump returned error: %v", err)
	}
	if requestedLength != entry.Size || !truncated {
		t.Fatalf("metadata mismatch: requested=%d truncated=%t", requestedLength, truncated)
	}
	if len(data) == 0 {
		t.Fatalf("expected dump data")
	}
	if uint64(len(data)) != maxPoolDumpBytes {
		t.Fatalf("dump length mismatch: got %d, want %d", len(data), maxPoolDumpBytes)
	}
	if !slices.Equal(calls[:2], []uint64{0, protocol.MaxReadLength}) {
		t.Fatalf("chunk offsets mismatch: %#v", calls[:2])
	}
}

func TestReadPoolDumpKeepsPartialDataAfterLaterReadFailure(t *testing.T) {
	previous := readContent
	defer func() {
		readContent = previous
	}()

	readContent = func(address uint64, offset uint64, length uint32) ([]byte, error) {
		if offset != 0 {
			return nil, errors.New("page no longer readable")
		}
		return []byte{1, 2, 3}, nil
	}

	data, _, truncated, err := readPoolDump(protocol.Entry{Address: 0x3000, Size: 0x2000})
	if err != nil {
		t.Fatalf("readPoolDump returned error: %v", err)
	}
	if !truncated || !slices.Equal(data, []byte{1, 2, 3}) {
		t.Fatalf("partial dump mismatch: truncated=%t data=%#v", truncated, data)
	}
}

func TestArrowKeyTypesMoveSelection(t *testing.T) {
	model := startedTestModel()
	model.diffReady = true
	model.diff = []protocol.Entry{
		{Address: 0x1000},
		{Address: 0x2000},
		{Address: 0x3000},
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = updated.(Model)
	if model.selected != 1 {
		t.Fatalf("down arrow did not move selection: got %d", model.selected)
	}

	updated, _ = model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model = updated.(Model)
	if model.selected != 0 {
		t.Fatalf("up arrow did not move selection: got %d", model.selected)
	}
}

func TestSelectionScrollsTableIntoView(t *testing.T) {
	model := startedTestModel()
	model.height = 28
	model.diffReady = true
	for index := 0; index < 20; index++ {
		model.diff = append(model.diff, protocol.Entry{Address: uint64(0x1000 + index*0x1000)})
	}

	for index := 0; index < 8; index++ {
		updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyDown})
		model = updated.(Model)
	}
	if model.selected != 8 {
		t.Fatalf("selection mismatch: got %d", model.selected)
	}
	if model.scrollOffset == 0 {
		t.Fatalf("scroll offset should move to keep selected row visible")
	}
}

func TestBaselineKeyRequiresStartedDriver(t *testing.T) {
	updated, cmd := newTestModel().Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	if cmd != nil {
		t.Fatalf("baseline before start should not return a command")
	}
	got := updated.(Model)
	if !containsLog(got.logs, "baseline skipped: start/load the driver first") {
		t.Fatalf("baseline guard log missing: %#v", got.logs)
	}
}

func TestEscapeClosesDumpBeforePTEDetails(t *testing.T) {
	model := newTestModel()
	model.dumpEntry = protocol.Entry{Address: 0x1000, Size: 0x1000}
	model.dumpData = []byte{0x41}
	model.pteDetails = device.PTEDetailsResult{
		Pages: []protocol.PTEPage{{VirtualAddress: 0x1000}},
	}

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("esc should not return a command while closing dump")
	}
	got := updated.(Model)
	if len(got.dumpData) != 0 {
		t.Fatalf("dump data was not cleared: %#v", got.dumpData)
	}
	if len(got.pteDetails.Pages) != 1 {
		t.Fatalf("pte details should remain visible: %#v", got.pteDetails.Pages)
	}
}

func TestEscapeClosesPTEDetailsWhenDumpIsClosed(t *testing.T) {
	model := newTestModel()
	model.pteDetails = device.PTEDetailsResult{
		Pages: []protocol.PTEPage{{VirtualAddress: 0x1000}},
	}

	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := updated.(Model)
	if len(got.pteDetails.Pages) != 0 {
		t.Fatalf("pte details were not cleared: %#v", got.pteDetails.Pages)
	}
}

func TestEscapeDoesNotQuitOnMainScreen(t *testing.T) {
	_, cmd := newTestModel().Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("esc on the main screen should not quit or run a command")
	}
}

func TestQuitKeysWorkOutsideInputMode(t *testing.T) {
	for _, key := range []string{"q", "ctrl+c", "ctrl+q"} {
		model := newTestModel()
		_, cmd := model.Update(tea.KeyMsg{Type: keyType(key), Runes: keyRunes(key)})
		if cmd == nil {
			t.Fatalf("expected quit command for %s", key)
		}
	}
}

func TestControlQuitKeysWorkInsideInputMode(t *testing.T) {
	for _, key := range []string{"ctrl+c", "ctrl+q"} {
		model := newTestModel()
		model.inputMode = "ascii"
		model.searchBox.Focus()
		_, cmd := model.Update(tea.KeyMsg{Type: keyType(key)})
		if cmd == nil {
			t.Fatalf("expected input-mode quit command for %s", key)
		}
	}
}

func TestEscapeCancelsInputMode(t *testing.T) {
	model := newTestModel()
	model.inputMode = "ascii"
	model.searchBox.Focus()

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("esc in input mode should cancel input, not quit")
	}
	got := updated.(Model)
	if got.inputMode != "" {
		t.Fatalf("input mode was not cleared: %q", got.inputMode)
	}
}

func TestPTEFlagFormatting(t *testing.T) {
	got := formatPTEFlags(protocol.PTEFlagPresent | protocol.PTEFlagReadable | protocol.PTEFlagExecutable)
	if got != "P|R|X" {
		t.Fatalf("pte flags mismatch: got %q", got)
	}
}

func keyType(value string) tea.KeyType {
	switch value {
	case "q":
		return tea.KeyRunes
	case "esc":
		return tea.KeyEsc
	case "ctrl+c":
		return tea.KeyCtrlC
	case "ctrl+q":
		return tea.KeyCtrlQ
	default:
		return tea.KeyRunes
	}
}

func keyRunes(value string) []rune {
	if value == "q" {
		return []rune{'q'}
	}
	return nil
}

func newTestModel() Model {
	model := NewModel()
	model.filterTag = ""
	model.filterWritable = triAny
	model.filterExecutable = triAny
	model.filterAccessible = triAny
	model.sortMode = sortAddress
	model.searchTarget = protocol.SearchTargetDiff
	model.tableView = tableViewDiff
	model.hashMode = protocol.HashModeSample
	model.hashSampleBytes = defaultHashSampleBytes
	return model
}

func startedTestModel() Model {
	model := newTestModel()
	model.status = scm.DriverStatus{Installed: true, State: svc.Running, StateText: "running"}
	return model
}

func containsLog(logs []string, want string) bool {
	for _, line := range logs {
		if line == want {
			return true
		}
	}
	return false
}

func containsLogPrefix(logs []string, want string) bool {
	for _, line := range logs {
		if strings.HasPrefix(line, want) {
			return true
		}
	}
	return false
}
