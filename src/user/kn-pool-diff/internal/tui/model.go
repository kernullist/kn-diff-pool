package tui

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"syscall"
	"unicode/utf16"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/sys/windows/svc"

	"kn-diff-pool/kn-pool-diff/internal/codesign"
	appconfig "kn-diff-pool/kn-pool-diff/internal/config"
	"kn-diff-pool/kn-pool-diff/internal/device"
	"kn-diff-pool/kn-pool-diff/internal/exporter"
	"kn-diff-pool/kn-pool-diff/internal/paths"
	"kn-diff-pool/kn-pool-diff/internal/privilege"
	"kn-diff-pool/kn-pool-diff/internal/protocol"
	"kn-diff-pool/kn-pool-diff/internal/scm"
)

const dumpPageSize = uint64(256)
const defaultHashSampleBytes = uint64(0x10000)
const tableRenderLimit = 10

const (
	tableViewDiff    = "diff"
	tableViewCurrent = "current"
)

type triState uint8

const (
	triAny triState = iota
	triYes
	triNo
)

const (
	sortAddress = iota
	sortPagesDesc
)

type Model struct {
	width            int
	height           int
	scrollOffset     int
	driverPath       string
	admin            privilege.Status
	signing          codesign.Status
	status           scm.DriverStatus
	ping             *protocol.PingResponse
	deviceText       string
	baseline         uint32
	baselineReady    bool
	currentTotal     uint32
	currentReady     bool
	currentEntries   []protocol.Entry
	diffTotal        uint32
	diffReady        bool
	diff             []protocol.Entry
	selected         int
	tableView        string
	dumpEntry        protocol.Entry
	dumpOffset       uint64
	dumpData         []byte
	pteEntry         protocol.Entry
	pteDetails       device.PTEDetailsResult
	inputMode        string
	searchBox        textinput.Model
	search           []protocol.SearchResult
	searchIndex      int
	searchText       string
	searchKind       string
	searchTotal      uint32
	searchStats      device.SearchContentResult
	searchTarget     uint32
	truncated        bool
	filterTag        string
	filterWritable   triState
	filterExecutable triState
	filterAccessible triState
	sortMode         int
	configPath       string
	exportDir        string
	hashMode         uint32
	hashSampleBytes  uint64
	busy             string
	logs             []string
}

type statusMsg struct {
	status scm.DriverStatus
	err    error
}

type environmentMsg struct {
	admin   privilege.Status
	signing codesign.Status
}

type actionMsg struct {
	title string
	err   error
}

type pingMsg struct {
	response protocol.PingResponse
	err      error
}

type snapshotMsg struct {
	title          string
	count          uint32
	total          uint32
	entries        []protocol.Entry
	currentTotal   uint32
	currentEntries []protocol.Entry
	err            error
}

type currentEntriesMsg struct {
	entries []protocol.Entry
	total   uint32
	err     error
}

type dumpMsg struct {
	entry  protocol.Entry
	offset uint64
	data   []byte
	err    error
}

type searchMsg struct {
	kind      string
	text      string
	results   []protocol.SearchResult
	total     uint32
	stats     device.SearchContentResult
	truncated bool
	err       error
}

type exportMsg struct {
	format string
	path   string
	err    error
}

type saveDumpMsg struct {
	path string
	err  error
}

type pteMsg struct {
	entry   protocol.Entry
	details device.PTEDetailsResult
	err     error
}

type shutdownMsg struct {
	result scm.UnloadResult
	err    error
}

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("81"))
	keyStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("229"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warnStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
)

func NewModel() Model {
	searchBox := textinput.New()
	searchBox.CharLimit = protocol.MaxSearchPattern * 3
	searchBox.Width = 48

	cfg, cfgPath, cfgErr := appconfig.Load()
	driverPath := paths.DefaultDriverPath()
	if strings.TrimSpace(cfg.DriverPath) != "" {
		driverPath = paths.Resolve(cfg.DriverPath)
	}

	exportDir := exporter.DefaultDir()
	if strings.TrimSpace(cfg.ExportDir) != "" {
		exportDir = paths.Resolve(cfg.ExportDir)
	}

	searchTarget := protocol.SearchTargetDiff
	if cfg.SearchTarget == protocol.SearchTargetCurrent {
		searchTarget = protocol.SearchTargetCurrent
	}

	tableView := tableViewDiff
	if cfg.TableView == tableViewCurrent {
		tableView = tableViewCurrent
	}

	hashMode := protocol.HashModeSample
	if cfg.HashMode == protocol.HashModeFull {
		hashMode = protocol.HashModeFull
	}

	hashSampleBytes := cfg.HashSampleBytes
	if hashSampleBytes == 0 {
		hashSampleBytes = defaultHashSampleBytes
	}

	logs := []string{"ready"}
	if cfgErr != nil {
		logs = append(logs, "config load failed: "+cfgErr.Error())
	}

	return Model{
		driverPath:       driverPath,
		admin:            privilege.Query(),
		signing:          codesign.Query(),
		deviceText:       "not checked",
		searchTarget:     searchTarget,
		searchBox:        searchBox,
		filterTag:        strings.TrimSpace(cfg.FilterTag),
		filterWritable:   validTriState(cfg.FilterWritable),
		filterExecutable: validTriState(cfg.FilterExecutable),
		filterAccessible: validTriState(cfg.FilterAccessible),
		sortMode:         validSortMode(cfg.SortMode, cfg.SortSize),
		configPath:       cfgPath,
		exportDir:        exportDir,
		tableView:        tableView,
		hashMode:         hashMode,
		hashSampleBytes:  hashSampleBytes,
		logs:             logs,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(queryStatusCmd(), environmentCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		if m.inputMode != "" {
			switch msg.String() {
			case "ctrl+c", "ctrl+q":
				return m.beginShutdown()
			case "esc":
				m.inputMode = ""
				m.searchBox.Blur()
				return m, nil
			case "enter":
				kind := m.inputMode
				value := m.searchBox.Value()
				m.inputMode = ""
				m.searchBox.Blur()
				if kind == "tag" {
					m.filterTag = strings.TrimSpace(value)
					m.selected = clampSelection(m.selected, len(m.visibleTable()))
					m.searchIndex = clampSelection(m.searchIndex, len(m.visibleSearch()))
					m.persistConfig()
					if m.filterTag == "" {
						m.addLog("tag filter cleared")
					} else {
						m.addLog("tag filter: " + m.filterTag)
					}
					return m, nil
				}
				m.beginCommand("searching", fmt.Sprintf("%s search requested", kind))
				return m, searchCmd(kind, value, m.searchTarget)
			}

			var cmd tea.Cmd
			m.searchBox, cmd = m.searchBox.Update(msg)
			return m, cmd
		}

		if m.busy == "unloading driver" {
			return m, nil
		}

		key := msg.String()
		switch {
		case key == "q" || key == "ctrl+c" || key == "ctrl+q":
			return m.beginShutdown()
		case key == "esc" || msg.Type == tea.KeyEsc:
			m.closeDetailPanel()
			return m, nil
		case key == "pgdown" || msg.Type == tea.KeyPgDown:
			m.scrollOffset += scrollStep(m.height)
			return m, nil
		case key == "pgup" || msg.Type == tea.KeyPgUp:
			m.scrollOffset -= scrollStep(m.height)
			if m.scrollOffset < 0 {
				m.scrollOffset = 0
			}
			return m, nil
		case key == "home" || msg.Type == tea.KeyHome:
			m.scrollOffset = 0
			return m, nil
		case key == "end" || msg.Type == tea.KeyEnd:
			m.scrollOffset = 1 << 30
			return m, nil
		case key == "up" || key == "k" || msg.Type == tea.KeyUp:
			if m.selected > 0 {
				m.selected--
			}
			m.scrollToSelectedTable()
			return m, nil
		case key == "down" || key == "j" || msg.Type == tea.KeyDown:
			if m.selected+1 < len(m.visibleTable()) {
				m.selected++
			}
			m.scrollToSelectedTable()
			return m, nil
		case key == "n":
			visibleSearch := m.visibleSearch()
			if len(visibleSearch) > 0 {
				m.searchIndex = (m.searchIndex + 1) % len(visibleSearch)
			}
			return m, nil
		case key == "s":
			if m.hasDriverStarted() {
				return m, nil
			}
			if !m.canManageService("start") {
				return m, nil
			}
			if !paths.Exists(m.driverPath) {
				m.addLog("start skipped: driver path not found: " + m.driverPath)
				return m, nil
			}
			m.beginCommand("installing and loading", "start requested: installing service and loading driver")
			return m, actionCmd("start", func() error {
				if err := scm.Install(m.driverPath); err != nil {
					return err
				}
				return scm.Start()
			})
		case key == "t":
			if !m.canManageService("stop") {
				return m, nil
			}
			m.beginCommand("unloading and deleting service", "stop requested: unloading driver and deleting service")
			return m, actionCmd("stop", func() error {
				return device.RunWithoutOpenHandles(scm.Delete)
			})
		case key == "b":
			if !m.hasDriverStarted() {
				m.addLog("baseline skipped: start/load the driver first")
				return m, nil
			}
			m.beginCommand("capturing baseline", "baseline requested")
			return m, baselineCmd()
		case key == "c":
			if !m.hasDriverStarted() {
				m.addLog("current skipped: start/load the driver first")
				return m, nil
			}
			if !m.hasBaselineSnapshot() {
				m.addLog("current skipped: capture baseline first")
				return m, nil
			}
			m.beginCommand("capturing current", "current requested")
			return m, currentCmd()
		case key == "l":
			if !m.hasDiffSnapshot() {
				m.addLog("diff skipped: capture current first")
				return m, nil
			}
			m.beginCommand("loading diff", "diff reload requested")
			return m, diffCmd()
		case key == "enter" || key == "x" || msg.Type == tea.KeyEnter:
			if !m.hasDiffSnapshot() {
				m.addLog("dump skipped: capture current first")
				return m, nil
			}
			visible := m.visibleTable()
			if len(visible) == 0 {
				m.addLog("dump skipped: no table entry selected")
				return m, nil
			}
			entry := visible[m.selected]
			m.beginCommand("dumping", fmt.Sprintf("dump requested: 0x%016X", entry.Address))
			return m, dumpCmd(entry, 0)
		case key == "g":
			if !m.hasDiffSnapshot() {
				m.addLog("jump skipped: capture current first")
				return m, nil
			}
			visibleSearch := m.visibleSearch()
			if len(visibleSearch) == 0 {
				m.addLog("jump skipped: no search result selected")
				return m, nil
			}
			result := visibleSearch[m.searchIndex]
			m.selected = findEntryIndex(m.visibleTable(), result.Address)
			m.beginCommand("dumping match", fmt.Sprintf("dump search hit requested: 0x%016X+0x%X", result.Address, result.Offset))
			return m, dumpSearchResultCmd(result)
		case key == "]":
			if len(m.dumpData) == 0 {
				return m, nil
			}
			nextOffset := m.dumpOffset + dumpPageSize
			if nextOffset < m.dumpOffset || nextOffset >= m.dumpEntry.Size {
				return m, nil
			}
			m.beginCommand("dumping next page", "dump next page requested")
			return m, dumpCmd(m.dumpEntry, nextOffset)
		case key == "[":
			if len(m.dumpData) == 0 {
				return m, nil
			}
			prevOffset := uint64(0)
			if m.dumpOffset > dumpPageSize {
				prevOffset = m.dumpOffset - dumpPageSize
			}
			m.beginCommand("dumping previous page", "dump previous page requested")
			return m, dumpCmd(m.dumpEntry, prevOffset)
		case key == "/":
			if !m.hasDiffSnapshot() {
				m.addLog("search skipped: capture current first")
				return m, nil
			}
			m.inputMode = "ascii"
			m.searchBox.Placeholder = "ASCII text"
			m.searchBox.SetValue("")
			m.searchBox.Focus()
			return m, nil
		case key == "m":
			if !m.hasDiffSnapshot() {
				m.addLog("search target skipped: capture current first")
				return m, nil
			}
			m.searchTarget = toggleSearchTarget(m.searchTarget)
			m.persistConfig()
			m.addLog("search target: " + protocol.SearchTargetName(m.searchTarget))
			return m, nil
		case key == "tab" || msg.Type == tea.KeyTab:
			if !m.hasDiffSnapshot() {
				m.addLog("table skipped: capture current first")
				return m, nil
			}
			m.tableView = toggleTableView(m.tableView)
			m.selected = clampSelection(m.selected, len(m.visibleTable()))
			m.persistConfig()
			m.addLog("table view: " + m.tableView)
			if m.tableView == tableViewCurrent && len(m.currentEntries) == 0 {
				m.beginCommand("loading current entries", "current table load requested")
				return m, currentEntriesCmd()
			}
			return m, nil
		case key == "d":
			if !m.hasDiffSnapshot() {
				m.addLog("pte skipped: capture current first")
				return m, nil
			}
			visible := m.visibleTable()
			if len(visible) == 0 {
				m.addLog("pte skipped: no table entry selected")
				return m, nil
			}
			entry := visible[m.selected]
			m.beginCommand("querying pte", fmt.Sprintf("pte requested: 0x%016X", entry.Address))
			return m, pteDetailsCmd(entry)
		case key == "h":
			if !m.hasDiffSnapshot() {
				m.addLog("search skipped: capture current first")
				return m, nil
			}
			m.inputMode = "hex"
			m.searchBox.Placeholder = "DE AD BE EF"
			m.searchBox.SetValue("")
			m.searchBox.Focus()
			return m, nil
		case key == "u":
			if !m.hasDiffSnapshot() {
				m.addLog("search skipped: capture current first")
				return m, nil
			}
			m.inputMode = "utf16"
			m.searchBox.Placeholder = "UTF-16LE text"
			m.searchBox.SetValue("")
			m.searchBox.Focus()
			return m, nil
		case key == "f":
			if !m.hasDiffSnapshot() {
				m.addLog("filter skipped: capture current first")
				return m, nil
			}
			m.inputMode = "tag"
			m.searchBox.Placeholder = "tag filter, empty clears"
			m.searchBox.SetValue(m.filterTag)
			m.searchBox.Focus()
			return m, nil
		case key == "w":
			if !m.hasDiffSnapshot() {
				m.addLog("filter skipped: capture current first")
				return m, nil
			}
			m.filterWritable = nextTriState(m.filterWritable)
			m.selected = clampSelection(m.selected, len(m.visibleTable()))
			m.searchIndex = clampSelection(m.searchIndex, len(m.visibleSearch()))
			m.persistConfig()
			m.addLog("writable filter: " + triStateText(m.filterWritable))
			return m, nil
		case key == "e":
			if !m.hasDiffSnapshot() {
				m.addLog("filter skipped: capture current first")
				return m, nil
			}
			m.filterExecutable = nextTriState(m.filterExecutable)
			m.selected = clampSelection(m.selected, len(m.visibleTable()))
			m.searchIndex = clampSelection(m.searchIndex, len(m.visibleSearch()))
			m.persistConfig()
			m.addLog("executable filter: " + triStateText(m.filterExecutable))
			return m, nil
		case key == "a":
			if !m.hasDiffSnapshot() {
				m.addLog("filter skipped: capture current first")
				return m, nil
			}
			m.filterAccessible = nextTriState(m.filterAccessible)
			m.selected = clampSelection(m.selected, len(m.visibleTable()))
			m.searchIndex = clampSelection(m.searchIndex, len(m.visibleSearch()))
			m.persistConfig()
			m.addLog("access filter: " + triStateText(m.filterAccessible))
			return m, nil
		case key == "o":
			if !m.hasDiffSnapshot() {
				m.addLog("sort skipped: capture current first")
				return m, nil
			}
			m.sortMode = nextSortMode(m.sortMode)
			m.selected = clampSelection(m.selected, len(m.visibleTable()))
			m.searchIndex = clampSelection(m.searchIndex, len(m.visibleSearch()))
			m.persistConfig()
			m.addLog("sort: " + sortModeText(m.sortMode))
			return m, nil
		case key == "r":
			if !m.hasDiffSnapshot() {
				m.addLog("export skipped: capture current first")
				return m, nil
			}
			m.beginCommand("exporting", "export requested")
			return m, exportAllCmd(m.exportDir, m.visibleDiff(), m.visibleCurrent(), m.visibleSearch(), m.exportSearchTarget(), m.searchKind, m.searchText, m.tableView, protocol.HashModeName(m.hashMode))
		case key == "v":
			if len(m.dumpData) == 0 {
				m.addLog("dump save skipped: dump a pool first with [enter] or [x]")
				return m, nil
			}
			m.beginCommand("saving dump", "dump save requested")
			return m, saveDumpCmd(m.exportDir, m.dumpEntry, m.dumpOffset, m.dumpData)
		}
	case statusMsg:
		wasRefreshing := m.busy == "refreshing"
		m.busy = ""
		if msg.err != nil {
			m.status = scm.DriverStatus{Installed: false, StateText: "unknown"}
			if wasRefreshing {
				m.addLog("refresh failed: " + friendlyError(msg.err))
			}
		} else {
			m.status = msg.status
			if wasRefreshing {
				m.addLog("refresh ok: " + serviceLogText(msg.status))
			}
		}
		return m, nil
	case environmentMsg:
		m.admin = msg.admin
		m.signing = msg.signing
		return m, nil
	case actionMsg:
		m.busy = ""
		if msg.err != nil {
			m.addLog(msg.title + " failed: " + friendlyError(msg.err))
			return m, tea.Batch(queryStatusCmd(), environmentCmd())
		} else {
			m.addLog(actionSuccessText(msg.title))
		}
		return m, m.actionFollowupCmd(msg.title)
	case pingMsg:
		wasPinging := m.busy == "pinging"
		m.busy = ""
		if msg.err != nil {
			m.ping = nil
			m.deviceText = "unavailable"
			if wasPinging {
				m.addLog("ping failed: " + friendlyError(msg.err))
			}
		} else {
			m.ping = &msg.response
			m.deviceText = fmt.Sprintf("connected, driver v%d.%d", msg.response.DriverVersionMajor, msg.response.DriverVersionMinor)
			if wasPinging {
				m.addLog(fmt.Sprintf("ping ok: driver v%d.%d", msg.response.DriverVersionMajor, msg.response.DriverVersionMinor))
			}
		}
		return m, nil
	case snapshotMsg:
		m.busy = ""
		if msg.err != nil {
			m.addLog(msg.title + " failed: " + friendlyError(msg.err))
			return m, nil
		}

		switch msg.title {
		case "baseline":
			refreshed := m.hasBaselineSnapshot()
			m.scrollOffset = 0
			m.baseline = msg.count
			m.baselineReady = true
			m.currentTotal = 0
			m.currentReady = false
			m.currentEntries = nil
			m.diffTotal = 0
			m.diffReady = false
			m.diff = nil
			m.selected = 0
			m.tableView = tableViewDiff
			m.clearAnalysisState()
			if refreshed {
				m.addLog(fmt.Sprintf("baseline refreshed: %d big pools", msg.count))
			} else {
				m.addLog(fmt.Sprintf("baseline captured: %d big pools", msg.count))
			}
		case "current":
			m.scrollOffset = 0
			m.diffTotal = msg.total
			m.diffReady = true
			m.diff = msg.entries
			m.currentTotal = msg.currentTotal
			m.currentReady = true
			m.currentEntries = msg.currentEntries
			m.selected = clampSelection(m.selected, len(m.visibleTable()))
			m.clearAnalysisState()
			m.addLog(fmt.Sprintf("current captured: %d current big pools, %d new big pools", msg.currentTotal, msg.total))
		case "diff":
			m.scrollOffset = 0
			m.diffTotal = msg.total
			m.diffReady = true
			m.currentReady = true
			m.diff = msg.entries
			m.selected = clampSelection(m.selected, len(m.visibleTable()))
			m.clearAnalysisState()
			m.addLog(fmt.Sprintf("diff loaded: %d/%d entries", len(msg.entries), msg.total))
		}

		return m, nil
	case currentEntriesMsg:
		m.busy = ""
		if msg.err != nil {
			m.addLog("current entries failed: " + friendlyError(msg.err))
		} else {
			m.scrollOffset = 0
			m.currentEntries = msg.entries
			m.currentTotal = msg.total
			m.currentReady = true
			m.selected = clampSelection(m.selected, len(m.visibleTable()))
			m.addLog(fmt.Sprintf("current loaded: %d/%d entries", len(msg.entries), msg.total))
		}
		return m, nil
	case dumpMsg:
		m.busy = ""
		if msg.err != nil {
			m.addLog("dump failed: " + friendlyError(msg.err))
		} else {
			m.scrollOffset = 1 << 30
			m.dumpEntry = msg.entry
			m.dumpOffset = msg.offset
			m.dumpData = msg.data
			m.addLog(fmt.Sprintf("dumped %d bytes from 0x%016X+0x%X", len(msg.data), msg.entry.Address, msg.offset))
		}
		return m, nil
	case searchMsg:
		m.busy = ""
		if msg.err != nil {
			m.addLog("search failed: " + friendlyError(msg.err))
		} else {
			m.scrollOffset = 1 << 30
			m.searchKind = msg.kind
			m.searchText = msg.text
			m.search = msg.results
			m.searchIndex = 0
			m.searchTotal = msg.total
			m.searchStats = msg.stats
			m.truncated = msg.truncated
			m.addLog(fmt.Sprintf(
				"search %s/%s: %d matches, skipped=%d read_failures=%d",
				msg.kind,
				protocol.SearchTargetName(msg.stats.Target),
				msg.total,
				msg.stats.EntriesSkipped,
				msg.stats.ReadFailures))
		}
		return m, nil
	case pteMsg:
		m.busy = ""
		if msg.err != nil {
			m.addLog("pte failed: " + friendlyError(msg.err))
		} else {
			m.scrollOffset = 1 << 30
			m.pteEntry = msg.entry
			m.pteDetails = msg.details
			m.addLog(fmt.Sprintf(
				"pte loaded: 0x%016X pages=%d/%d",
				msg.details.Address,
				msg.details.ReturnedPages,
				msg.details.PageCount))
		}
		return m, nil
	case exportMsg:
		m.busy = ""
		if msg.err != nil {
			m.addLog(msg.format + " export failed: " + friendlyError(msg.err))
		} else {
			m.addLog(msg.format + " exported: " + msg.path)
		}
		return m, nil
	case saveDumpMsg:
		m.busy = ""
		if msg.err != nil {
			m.addLog("dump save failed: " + friendlyError(msg.err))
		} else {
			m.addLog("dump saved: " + msg.path)
		}
		return m, nil
	case shutdownMsg:
		m.busy = ""
		if msg.err != nil {
			m.addLog("exit blocked: cleanup failed: " + friendlyError(msg.err))
			return m, nil
		}
		if msg.result.Deleted {
			m.addLog("driver stopped and service deleted; exiting")
		} else if msg.result.WasRunning {
			m.addLog("driver stopped; exiting")
		} else if msg.result.Installed {
			m.addLog("driver service already stopped; exiting")
		} else {
			m.addLog("driver service not installed; exiting")
		}
		return m, tea.Quit
	}

	return m, nil
}

func (m Model) View() string {
	var body strings.Builder
	visibleDiff := m.visibleDiff()
	visibleCurrent := m.visibleCurrent()
	visibleTable := m.visibleTable()
	visibleSearch := m.visibleSearch()

	body.WriteString(titleStyle.Render("KN Diff Pool"))
	body.WriteString("\n")
	body.WriteString(dimStyle.Render("Windows kernel Big Pool snapshot/diff tool"))
	body.WriteString("\n\n")

	body.WriteString(row("Service", serviceText(m.status)))
	body.WriteString(row("Admin", adminText(m.admin)))
	body.WriteString(row("TestSign", signingText(m.signing)))
	body.WriteString(row("Device", deviceText(m.deviceText)))
	body.WriteString(row("Driver sys", driverPathText(m.driverPath)))
	body.WriteString(row("Export dir", m.exportDir))
	body.WriteString(row("Baseline", fmt.Sprintf("%d big pools", m.baseline)))
	body.WriteString(row("Current", fmt.Sprintf("%d big pools, %d visible", m.currentTotal, len(visibleCurrent))))
	body.WriteString(row("Diff", fmt.Sprintf("%d entries, %d visible", m.diffTotal, len(visibleDiff))))
	body.WriteString(row("Table", fmt.Sprintf("%s, %d visible", m.tableView, len(visibleTable))))
	body.WriteString(row("Search", protocol.SearchTargetName(m.searchTarget)))
	body.WriteString(row("Filters", filtersText(m)))
	body.WriteString(row("Sort", sortModeText(m.sortMode)))
	if m.busy != "" {
		body.WriteString(row("Busy", warnStyle.Render(m.busy)))
	}

	if len(visibleTable) > 0 {
		body.WriteString(titleStyle.Render(tableTitle(m.tableView)))
		body.WriteString("\n")
		body.WriteString(renderEntryTable(visibleTable, tableRenderLimit, m.selected))
		body.WriteString("\n")
		if entry, ok := selectedEntry(visibleTable, m.selected); ok {
			body.WriteString(titleStyle.Render("Selected Pool"))
			body.WriteString("\n")
			body.WriteString(renderPoolDetail(entry))
			body.WriteString("\n")
		}
	}

	if len(m.dumpData) > 0 {
		body.WriteString(titleStyle.Render("Dump"))
		body.WriteString("\n")
		body.WriteString(fmt.Sprintf(
			"base 0x%016X  offset 0x%X  pages %s  hash 0x%016X/%d\n",
			m.dumpEntry.Address,
			m.dumpOffset,
			pageSummary(m.dumpEntry),
			m.dumpEntry.ContentHash,
			m.dumpEntry.HashedBytes))
		body.WriteString(renderHexDump(addOffset(m.dumpEntry.Address, m.dumpOffset), m.dumpData, 8))
		body.WriteString("\n")
	}

	if len(m.pteDetails.Pages) > 0 {
		body.WriteString(titleStyle.Render("PTE Details"))
		body.WriteString("\n")
		body.WriteString(renderPTEDetails(m.pteEntry, m.pteDetails, 10))
		body.WriteString("\n")
	}

	if m.searchText != "" {
		body.WriteString(titleStyle.Render("Search Results"))
		body.WriteString("\n")
		body.WriteString(renderSearchResults(m.searchKind, m.searchText, m.searchStats, visibleSearch, m.searchTotal, m.truncated, 8, m.searchIndex))
		body.WriteString("\n")
	}

	if m.inputMode != "" {
		body.WriteString(titleStyle.Render(inputTitle(m.inputMode)))
		body.WriteString("\n")
		body.WriteString(fmt.Sprintf("%s: %s\n\n", inputTitle(m.inputMode), m.searchBox.View()))
		body.WriteString(dimStyle.Render("esc cancels input. ctrl+c/ctrl+q quits."))
		body.WriteString("\n\n")
	}

	footer := renderFooter(m)
	return renderScreen(body.String(), footer, m.height, m.scrollOffset)
}

func queryStatusCmd() tea.Cmd {
	return func() tea.Msg {
		status, err := scm.Query()
		return statusMsg{status: status, err: err}
	}
}

func environmentCmd() tea.Cmd {
	return func() tea.Msg {
		return environmentMsg{
			admin:   privilege.Query(),
			signing: codesign.Query(),
		}
	}
}

func actionCmd(title string, fn func() error) tea.Cmd {
	return func() tea.Msg {
		return actionMsg{title: title, err: fn()}
	}
}

func pingCmd() tea.Cmd {
	return func() tea.Msg {
		response, err := device.Ping()
		return pingMsg{response: response, err: err}
	}
}

func baselineCmd() tea.Cmd {
	return func() tea.Msg {
		count, err := device.CreateBaseline()
		return snapshotMsg{title: "baseline", count: count, err: err}
	}
}

func currentCmd() tea.Cmd {
	return func() tea.Msg {
		count, err := device.CreateCurrent()
		if err != nil {
			return snapshotMsg{title: "current", err: err}
		}

		diffEntries, diffTotal, err := device.DiffEntries()
		if err != nil {
			return snapshotMsg{title: "current", count: count, total: count, err: err}
		}

		currentEntries, currentTotal, err := device.CurrentEntries()
		if err != nil {
			return snapshotMsg{title: "current", count: count, total: diffTotal, entries: diffEntries, err: err}
		}

		return snapshotMsg{
			title:          "current",
			count:          count,
			total:          diffTotal,
			entries:        diffEntries,
			currentTotal:   currentTotal,
			currentEntries: currentEntries,
		}
	}
}

func diffCmd() tea.Cmd {
	return func() tea.Msg {
		entries, total, err := device.DiffEntries()
		return snapshotMsg{title: "diff", total: total, entries: entries, err: err}
	}
}

func currentEntriesCmd() tea.Cmd {
	return func() tea.Msg {
		entries, total, err := device.CurrentEntries()
		return currentEntriesMsg{entries: entries, total: total, err: err}
	}
}

func dumpCmd(entry protocol.Entry, offset uint64) tea.Cmd {
	return func() tea.Msg {
		data, err := device.ReadContent(entry.Address, offset, uint32(dumpPageSize))
		return dumpMsg{entry: entry, offset: offset, data: data, err: err}
	}
}

func dumpSearchResultCmd(result protocol.SearchResult) tea.Cmd {
	return dumpCmd(protocol.Entry{
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
	}, result.Offset)
}

func searchCmd(kind, value string, target uint32) tea.Cmd {
	return func() tea.Msg {
		pattern, err := parseSearchPattern(kind, value)
		if err != nil {
			return searchMsg{kind: kind, text: value, err: err}
		}

		result, err := device.SearchContent(pattern, protocol.MaxSearchResults, target)
		return searchMsg{
			kind:      kind,
			text:      value,
			results:   result.Results,
			total:     result.TotalMatches,
			stats:     result,
			truncated: result.Truncated,
			err:       err,
		}
	}
}

func pteDetailsCmd(entry protocol.Entry) tea.Cmd {
	return func() tea.Msg {
		details, err := device.PTEDetails(entry.Address, protocol.MaxPTEDetailPages)
		return pteMsg{entry: entry, details: details, err: err}
	}
}

func exportCmd(dir string, format string, diff []protocol.Entry, current []protocol.Entry, search []protocol.SearchResult, searchTarget, searchKind, searchText, tableView, hashMode string) tea.Cmd {
	return func() tea.Msg {
		path, err := exporter.ExportAnalysis(dir, format, diff, current, search, searchTarget, searchKind, searchText, tableView, hashMode)
		return exportMsg{format: format, path: path, err: err}
	}
}

func exportAllCmd(dir string, diff []protocol.Entry, current []protocol.Entry, search []protocol.SearchResult, searchTarget, searchKind, searchText, tableView, hashMode string) tea.Cmd {
	return func() tea.Msg {
		jsonPath, err := exporter.ExportAnalysis(dir, "json", diff, current, search, searchTarget, searchKind, searchText, tableView, hashMode)
		if err != nil {
			return exportMsg{format: "analysis", err: fmt.Errorf("json: %w", err)}
		}

		csvPath, err := exporter.ExportAnalysis(dir, "csv", diff, current, search, searchTarget, searchKind, searchText, tableView, hashMode)
		if err != nil {
			return exportMsg{format: "analysis", path: jsonPath, err: fmt.Errorf("csv: %w", err)}
		}

		return exportMsg{format: "analysis", path: fmt.Sprintf("json=%s csv=%s", jsonPath, csvPath)}
	}
}

func saveDumpCmd(dir string, entry protocol.Entry, offset uint64, data []byte) tea.Cmd {
	return func() tea.Msg {
		path, err := exporter.SaveDump(dir, entry, offset, data)
		return saveDumpMsg{path: path, err: err}
	}
}

func shutdownCmd() tea.Cmd {
	return func() tea.Msg {
		var result scm.UnloadResult
		err := device.RunWithoutOpenHandles(func() error {
			var cleanupErr error
			result, cleanupErr = scm.StopAndDelete()
			return cleanupErr
		})
		return shutdownMsg{result: result, err: err}
	}
}

func row(name, value string) string {
	return fmt.Sprintf("%-10s %s\n", name+":", value)
}

func renderHelp(m Model) string {
	serviceLine := []string{}
	if m.hasDriverStarted() {
		serviceLine = append(serviceLine, "[t] stop/unload+delete")
	} else {
		serviceLine = append(serviceLine, "[s] start/install+load")
	}
	serviceLine = append(serviceLine, "[q] quit")

	lines := [][]string{serviceLine}

	if m.canGoBack() {
		lines[0] = append(lines[0], "[esc] back")
	}

	if m.hasDriverStarted() {
		snapshotLine := []string{"[b] baseline"}
		if m.hasBaselineSnapshot() {
			snapshotLine = append(snapshotLine, "[c] current")
		}
		if m.hasDiffSnapshot() {
			snapshotLine = append(snapshotLine, "[l] diff", "[tab] table")
		}
		lines = append(lines, snapshotLine)
	}

	if m.hasDriverStarted() && m.hasDiffSnapshot() {
		analysisLine := []string{}
		if len(m.visibleTable()) > 0 {
			analysisLine = append(analysisLine, "[up/down] select", "[enter/x] dump bytes", "[d] page details")
		}
		analysisLine = append(analysisLine, "[pgup/pgdn] scroll", "[home/end] jump")
		if len(m.visibleSearch()) > 0 {
			analysisLine = append(analysisLine, "[g] dump search hit", "[n] search select")
		}
		if len(analysisLine) > 0 {
			lines = append(lines, analysisLine)
		}

		filterLine := []string{"[/] ascii", "[h] hex", "[u] utf16", "[f] tag", "[w/e/a] filters", "[o] sort", "[r] export"}
		if len(m.dumpData) > 0 {
			filterLine = append(filterLine, "[v] save")
		}
		lines = append(lines, filterLine)
	}

	var b strings.Builder
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		for index, item := range line {
			if index > 0 {
				b.WriteString("  ")
			}
			space := strings.IndexByte(item, ' ')
			if space < 0 {
				b.WriteString(keyStyle.Render(item))
				continue
			}
			b.WriteString(keyStyle.Render(item[:space]))
			b.WriteString(item[space:])
		}
		b.WriteString("\n")
	}
	return b.String()
}

func renderFooter(m Model) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("Log"))
	b.WriteString("\n")
	b.WriteString(lastLogText(m))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("Run elevated for SCM operations. Test-signing must be enabled for unsigned test builds."))
	b.WriteString("\n")
	b.WriteString(renderHelp(m))
	return b.String()
}

func renderScreen(body string, footer string, height int, scrollOffset int) string {
	if height <= 0 {
		return strings.TrimRight(body, "\n") + "\n\n" + footer
	}

	bodyLines := splitLines(body)
	footerLines := splitLines(footer)
	bodyHeight := height - len(footerLines)
	if bodyHeight < 1 {
		bodyHeight = 1
	}

	maxOffset := len(bodyLines) - bodyHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}
	if scrollOffset > maxOffset {
		scrollOffset = maxOffset
	}

	end := scrollOffset + bodyHeight
	if end > len(bodyLines) {
		end = len(bodyLines)
	}
	visible := append([]string(nil), bodyLines[scrollOffset:end]...)
	for len(visible) < bodyHeight {
		visible = append(visible, "")
	}
	return strings.Join(append(visible, footerLines...), "\n")
}

func splitLines(value string) []string {
	value = strings.TrimRight(value, "\n")
	if value == "" {
		return nil
	}
	return strings.Split(value, "\n")
}

func scrollStep(height int) int {
	if height <= 4 {
		return 4
	}
	step := height / 2
	if step < 4 {
		return 4
	}
	return step
}

func (m *Model) scrollToSelectedTable() {
	if len(m.visibleTable()) == 0 {
		return
	}

	bodyHeight := m.bodyViewportHeight()
	line := m.selectedTableBodyLine()
	if line < m.scrollOffset {
		m.scrollOffset = line
		return
	}
	if line >= m.scrollOffset+bodyHeight {
		m.scrollOffset = line - bodyHeight + 1
	}
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
}

func (m Model) bodyViewportHeight() int {
	if m.height <= 0 {
		return 0
	}
	bodyHeight := m.height - len(splitLines(renderFooter(m)))
	if bodyHeight < 1 {
		return 1
	}
	return bodyHeight
}

func (m Model) selectedTableBodyLine() int {
	line := 0
	line += 2 // title and subtitle
	line++    // blank line after subtitle
	line += 13
	if m.busy != "" {
		line++
	}
	line++ // table title
	line++ // table header

	start := 0
	if m.selected >= tableRenderLimit {
		start = m.selected - tableRenderLimit + 1
	}
	return line + (m.selected - start)
}

func tableTitle(value string) string {
	if value == tableViewCurrent {
		return "Current Big Pool Snapshot"
	}
	return "New Big Pool Allocations"
}

func inputTitle(mode string) string {
	switch mode {
	case "driver":
		return "Driver Path"
	case "exportdir":
		return "Export Directory"
	case "tag":
		return "Tag Filter"
	case "ascii":
		return "ASCII Search"
	case "hex":
		return "Hex Search"
	case "utf16":
		return "UTF-16LE Search"
	default:
		return "Input"
	}
}

func serviceText(status scm.DriverStatus) string {
	if !status.Installed {
		return warnStyle.Render("not installed")
	}
	if status.StateText == "running" {
		return okStyle.Render(status.StateText)
	}
	return warnStyle.Render(status.StateText)
}

func signingText(status codesign.Status) string {
	if status.Err != nil {
		return errStyle.Render("unknown")
	}
	if status.TestSigning {
		return okStyle.Render(fmt.Sprintf("enabled (0x%X)", status.Options))
	}
	return warnStyle.Render(fmt.Sprintf("disabled (0x%X)", status.Options))
}

func adminText(status privilege.Status) string {
	if status.Err != nil {
		return errStyle.Render("unknown: " + status.Err.Error())
	}
	if status.Elevated {
		return okStyle.Render("elevated")
	}
	return warnStyle.Render("not elevated")
}

func driverPathText(path string) string {
	if paths.Exists(path) {
		return okStyle.Render(path)
	}
	return errStyle.Render(path + " (missing)")
}

func deviceText(value string) string {
	if strings.HasPrefix(value, "connected") {
		return okStyle.Render(value)
	}
	if value == "not checked" {
		return dimStyle.Render(value)
	}
	if strings.Contains(value, "stopped") || strings.Contains(value, "not running") {
		return warnStyle.Render(value)
	}
	return errStyle.Render(value)
}

func (m *Model) canManageService(action string) bool {
	if m.admin.Err != nil {
		m.addLog(action + " skipped: administrator status is unknown: " + m.admin.Err.Error())
		return false
	}
	if !m.admin.Elevated {
		m.addLog(action + " skipped: run the TUI as Administrator")
		return false
	}
	return true
}

func (m *Model) addLog(line string) {
	m.logs = []string{line}
}

func (m *Model) beginCommand(busy string, line string) {
	m.busy = busy
	m.addLog(line)
}

func (m *Model) actionFollowupCmd(title string) tea.Cmd {
	switch title {
	case "start":
		return tea.Batch(queryStatusCmd(), environmentCmd(), pingCmd())
	case "stop":
		m.ping = nil
		m.deviceText = "unavailable (service deleted)"
		m.clearSnapshotState()
		return tea.Batch(queryStatusCmd(), environmentCmd())
	default:
		return tea.Batch(queryStatusCmd(), environmentCmd())
	}
}

func (m Model) beginShutdown() (tea.Model, tea.Cmd) {
	if m.busy == "unloading driver" {
		return m, nil
	}

	m.inputMode = ""
	m.searchBox.Blur()
	if !m.canCleanupOnExit() {
		m.busy = ""
		m.addLog("exit requested: not elevated, skipping driver unload")
		return m, tea.Quit
	}

	m.busy = "unloading driver"
	m.addLog("exit requested: closing driver handles, stopping driver, deleting service")
	return m, shutdownCmd()
}

func (m Model) canCleanupOnExit() bool {
	return m.admin.Err == nil && m.admin.Elevated
}

func actionSuccessText(title string) string {
	switch title {
	case "start":
		return "start ok; service installed and driver loaded"
	case "stop":
		return "stop ok; driver unloaded and service deleted"
	default:
		return title + " ok"
	}
}

func serviceLogText(status scm.DriverStatus) string {
	if !status.Installed {
		return "not installed"
	}
	return "installed, " + status.StateText
}

func (m *Model) clearAnalysisState() {
	m.dumpEntry = protocol.Entry{}
	m.dumpOffset = 0
	m.dumpData = nil
	m.pteEntry = protocol.Entry{}
	m.pteDetails = device.PTEDetailsResult{}
	m.search = nil
	m.searchIndex = 0
	m.searchText = ""
	m.searchKind = ""
	m.searchTotal = 0
	m.searchStats = device.SearchContentResult{}
	m.truncated = false
}

func (m *Model) clearSnapshotState() {
	m.baseline = 0
	m.baselineReady = false
	m.currentTotal = 0
	m.currentReady = false
	m.currentEntries = nil
	m.diffTotal = 0
	m.diffReady = false
	m.diff = nil
	m.selected = 0
	m.clearAnalysisState()
}

func (m Model) hasDriverStarted() bool {
	return m.ping != nil ||
		strings.HasPrefix(m.deviceText, "connected") ||
		(m.status.Installed && m.status.State == svc.Running)
}

func (m Model) hasBaselineSnapshot() bool {
	return m.baselineReady || m.baseline > 0
}

func (m Model) hasCurrentSnapshot() bool {
	return m.currentReady || m.currentTotal > 0 || len(m.currentEntries) > 0
}

func (m Model) hasDiffSnapshot() bool {
	return m.diffReady || m.diffTotal > 0 || len(m.diff) > 0
}

func (m Model) canGoBack() bool {
	return m.inputMode != "" || len(m.dumpData) > 0 || len(m.pteDetails.Pages) > 0 || m.searchText != ""
}

func (m *Model) closeDetailPanel() bool {
	if len(m.dumpData) > 0 {
		m.dumpEntry = protocol.Entry{}
		m.dumpOffset = 0
		m.dumpData = nil
		m.addLog("dump closed")
		return true
	}
	if len(m.pteDetails.Pages) > 0 {
		m.pteEntry = protocol.Entry{}
		m.pteDetails = device.PTEDetailsResult{}
		m.addLog("pte details closed")
		return true
	}
	if m.searchText != "" {
		m.search = nil
		m.searchIndex = 0
		m.searchText = ""
		m.searchKind = ""
		m.searchTotal = 0
		m.searchStats = device.SearchContentResult{}
		m.truncated = false
		m.addLog("search results closed")
		return true
	}
	return false
}

func (m Model) visibleLogs() []string {
	return m.logs
}

func lastLogText(m Model) string {
	if len(m.logs) == 0 {
		return "ready"
	}
	return m.logs[len(m.logs)-1]
}

func renderEntryTable(entries []protocol.Entry, limit int, selected int) string {
	start := 0
	if selected >= limit {
		start = selected - limit + 1
	}
	end := start + limit
	if end > len(entries) {
		end = len(entries)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("  %-18s %-10s %-6s %-13s %-18s %s\n", "Address", "Size", "Tag", "a/w/x pages", "Hash", "Props"))
	for i := start; i < end; i++ {
		entry := entries[i]
		marker := " "
		if i == selected {
			marker = ">"
		}
		b.WriteString(fmt.Sprintf(
			"%s 0x%016X %-10s %-6s %-13s 0x%016X/%-5d %s\n",
			marker,
			entry.Address,
			formatSize(entry.Size),
			entry.TagString(),
			pageSummary(entry),
			entry.ContentHash,
			entry.HashedBytes,
			formatFlags(entry.Flags)))
	}

	if end < len(entries) {
		b.WriteString(dimStyle.Render(fmt.Sprintf("... %d more entries\n", len(entries)-end)))
	}

	return b.String()
}

func formatSize(size uint64) string {
	if size >= 1024*1024 {
		return fmt.Sprintf("%d MB", size/(1024*1024))
	}
	if size >= 1024 {
		return fmt.Sprintf("%d KB", size/1024)
	}
	return fmt.Sprintf("%d B", size)
}

func formatFlags(flags uint32) string {
	parts := []string{}
	if flags&protocol.FlagNonPaged != 0 {
		parts = append(parts, "NP")
	}
	if flags&protocol.FlagAccessible != 0 {
		parts = append(parts, "A")
	}
	if flags&protocol.FlagWritable != 0 {
		parts = append(parts, "W")
	}
	if flags&protocol.FlagExecutable != 0 {
		parts = append(parts, "X")
	}
	if flags&protocol.FlagAccessibleAll != 0 {
		parts = append(parts, "Aall")
	}
	if flags&protocol.FlagWritableAll != 0 {
		parts = append(parts, "Wall")
	}
	if flags&protocol.FlagExecutableAny != 0 {
		parts = append(parts, "Xany")
	}
	if flags&protocol.FlagWritableMixed != 0 {
		parts = append(parts, "Wmix")
	}
	if flags&protocol.FlagExecutableMixed != 0 {
		parts = append(parts, "Xmix")
	}
	if flags&protocol.FlagHashChanged != 0 {
		parts = append(parts, "Hchg")
	}
	if flags&protocol.FlagHashSampled != 0 {
		parts = append(parts, "Hsamp")
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "|")
}

func pageSummary(entry protocol.Entry) string {
	if entry.PageCount == 0 {
		return "-"
	}
	return fmt.Sprintf("a%d/w%d/x%d/%d", entry.AccessiblePages, entry.WritablePages, entry.ExecutablePages, entry.PageCount)
}

func selectedEntry(entries []protocol.Entry, selected int) (protocol.Entry, bool) {
	if len(entries) == 0 {
		return protocol.Entry{}, false
	}
	selected = clampSelection(selected, len(entries))
	return entries[selected], true
}

func renderPoolDetail(entry protocol.Entry) string {
	var b strings.Builder
	b.WriteString(row("Address", formatAddressRange(entry.Address, entry.Size)))
	b.WriteString(row("Size", fmt.Sprintf("%s (%d bytes)", formatSize(entry.Size), entry.Size)))
	b.WriteString(row("Tag", entry.TagString()))
	b.WriteString(row("Pages", formatPageDetails(entry)))
	b.WriteString(row("Props", formatFlagDescriptions(entry.Flags)))
	b.WriteString(row("Hash", formatHashDetails(entry)))
	b.WriteString(dimStyle.Render("a/w/x means accessible(readable), writable, executable pages. enter/x dumps bytes; d shows page-level PTE details.\n"))
	return b.String()
}

func formatAddressRange(address uint64, size uint64) string {
	if size == 0 {
		return fmt.Sprintf("0x%016X", address)
	}
	end := address + size - 1
	if end < address {
		return fmt.Sprintf("0x%016X - overflow", address)
	}
	return fmt.Sprintf("0x%016X - 0x%016X", address, end)
}

func formatPageDetails(entry protocol.Entry) string {
	if entry.PageCount == 0 {
		return "not reported"
	}
	return fmt.Sprintf(
		"accessible %d/%d, writable %d/%d, executable %d/%d",
		entry.AccessiblePages,
		entry.PageCount,
		entry.WritablePages,
		entry.PageCount,
		entry.ExecutablePages,
		entry.PageCount)
}

func formatFlagDescriptions(flags uint32) string {
	parts := []string{}
	if flags&protocol.FlagNonPaged != 0 {
		parts = append(parts, "nonpaged")
	}
	if flags&protocol.FlagAccessible != 0 {
		parts = append(parts, "first page accessible")
	}
	if flags&protocol.FlagWritable != 0 {
		parts = append(parts, "first page writable")
	}
	if flags&protocol.FlagExecutable != 0 {
		parts = append(parts, "first page executable")
	}
	if flags&protocol.FlagAccessibleAll != 0 {
		parts = append(parts, "all pages accessible")
	}
	if flags&protocol.FlagWritableAll != 0 {
		parts = append(parts, "all pages writable")
	}
	if flags&protocol.FlagExecutableAny != 0 {
		parts = append(parts, "has executable page")
	}
	if flags&protocol.FlagWritableMixed != 0 {
		parts = append(parts, "mixed writable pages")
	}
	if flags&protocol.FlagExecutableMixed != 0 {
		parts = append(parts, "mixed executable pages")
	}
	if flags&protocol.FlagHashChanged != 0 {
		parts = append(parts, "content hash changed")
	}
	if flags&protocol.FlagHashSampled != 0 {
		parts = append(parts, "hash sampled")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}

func formatHashDetails(entry protocol.Entry) string {
	scope := "full"
	if entry.Flags&protocol.FlagHashSampled != 0 {
		scope = "sampled"
	}
	return fmt.Sprintf("0x%016X over %d bytes (%s)", entry.ContentHash, entry.HashedBytes, scope)
}

func renderHexDump(base uint64, data []byte, lines int) string {
	var b strings.Builder
	maxBytes := lines * 16
	if len(data) < maxBytes {
		maxBytes = len(data)
	}

	for offset := 0; offset < maxBytes; offset += 16 {
		lineEnd := offset + 16
		if lineEnd > maxBytes {
			lineEnd = maxBytes
		}

		chunk := data[offset:lineEnd]
		b.WriteString(fmt.Sprintf("0x%016X  ", base+uint64(offset)))
		for i := 0; i < 16; i++ {
			if i < len(chunk) {
				b.WriteString(fmt.Sprintf("%02X ", chunk[i]))
			} else {
				b.WriteString("   ")
			}
		}
		b.WriteString(" ")
		for _, value := range chunk {
			if value >= 0x20 && value <= 0x7e {
				b.WriteByte(value)
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteString("\n")
	}

	if len(data) > maxBytes {
		b.WriteString(dimStyle.Render(fmt.Sprintf("... %d more bytes\n", len(data)-maxBytes)))
	}

	return b.String()
}

func renderPTEDetails(entry protocol.Entry, details device.PTEDetailsResult, limit int) string {
	var b strings.Builder
	suffix := ""
	if details.Truncated {
		suffix = " (truncated)"
	}
	b.WriteString(fmt.Sprintf(
		"pool 0x%016X %s tag %s pages %s returned=%d/%d%s\n",
		details.Address,
		formatSize(details.PoolSize),
		entry.TagString(),
		formatPageDetails(entry),
		details.ReturnedPages,
		details.PageCount,
		suffix))
	b.WriteString(dimStyle.Render("PTE flags: P=present, R=readable by safe copy, W=writable, X=executable, Large=large page.\n"))

	end := len(details.Pages)
	if end > limit {
		end = limit
	}
	b.WriteString(fmt.Sprintf("  %-5s %-18s %-12s %-18s %s\n", "Page", "Address", "Status", "PTE", "PTE Flags"))
	for i := 0; i < end; i++ {
		page := details.Pages[i]
		b.WriteString(fmt.Sprintf(
			"  %-5d 0x%016X %-12s 0x%016X %s\n",
			page.PageIndex,
			page.VirtualAddress,
			pteStatusText(page.Status),
			page.PTEValue,
			formatPTEFlags(page.Flags)))
	}
	if len(details.Pages) > end {
		b.WriteString(dimStyle.Render(fmt.Sprintf("... %d more pte pages\n", len(details.Pages)-end)))
	}
	return b.String()
}

func pteStatusText(status uint32) string {
	if status == 0 {
		return "ok"
	}
	return fmt.Sprintf("0x%08X", status)
}

func formatPTEFlags(flags uint32) string {
	parts := []string{}
	if flags&protocol.PTEFlagPresent != 0 {
		parts = append(parts, "P")
	}
	if flags&protocol.PTEFlagReadable != 0 {
		parts = append(parts, "R")
	}
	if flags&protocol.PTEFlagWritable != 0 {
		parts = append(parts, "W")
	}
	if flags&protocol.PTEFlagExecutable != 0 {
		parts = append(parts, "X")
	}
	if flags&protocol.PTEFlagLargePage != 0 {
		parts = append(parts, "Large")
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "|")
}

func renderSearchResults(kind, text string, stats device.SearchContentResult, results []protocol.SearchResult, total uint32, truncated bool, limit int, selected int) string {
	var b strings.Builder
	suffix := ""
	if truncated {
		suffix = " (truncated)"
	}
	b.WriteString(fmt.Sprintf(
		"%s/%s %q: %d matches%s, showing=%d, scanned=%d skipped=%d read_failures=%d\n",
		kind,
		protocol.SearchTargetName(stats.Target),
		text,
		total,
		suffix,
		len(results),
		stats.EntriesScanned,
		stats.EntriesSkipped,
		stats.ReadFailures))
	if len(results) == 0 {
		return b.String()
	}

	start := 0
	if selected >= limit {
		start = selected - limit + 1
	}
	end := start + limit
	if end > len(results) {
		end = len(results)
	}

	b.WriteString(fmt.Sprintf("  %-18s %-10s %-10s %-6s %-13s %s\n", "Address", "Offset", "PoolSize", "Tag", "a/w/x pages", "Props"))
	for i := start; i < end; i++ {
		result := results[i]
		marker := " "
		if i == selected {
			marker = ">"
		}
		b.WriteString(fmt.Sprintf(
			"%s 0x%016X 0x%-8X %-10s %-6s %-13s %s\n",
			marker,
			result.Address,
			result.Offset,
			formatSize(result.PoolSize),
			result.TagString(),
			pageSummary(searchResultEntry(result)),
			formatFlags(result.Flags)))
	}
	if end < len(results) {
		b.WriteString(dimStyle.Render(fmt.Sprintf("... %d more results\n", len(results)-end)))
	}
	return b.String()
}

func parseSearchPattern(kind, value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("empty pattern")
	}

	switch kind {
	case "ascii":
		return []byte(value), nil
	case "hex":
		cleaned := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "", ",", "", "0x", "", "0X", "").Replace(value)
		if len(cleaned)%2 != 0 {
			return nil, fmt.Errorf("hex pattern must contain an even number of digits")
		}
		decoded, err := hex.DecodeString(cleaned)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	case "utf16":
		encoded := utf16.Encode([]rune(value))
		pattern := make([]byte, 0, len(encoded)*2)
		for _, value := range encoded {
			pattern = append(pattern, byte(value), byte(value>>8))
		}
		return pattern, nil
	default:
		return nil, fmt.Errorf("unknown search mode %q", kind)
	}
}

func (m Model) visibleDiff() []protocol.Entry {
	return m.visibleEntries(m.diff)
}

func (m Model) visibleCurrent() []protocol.Entry {
	return m.visibleEntries(m.currentEntries)
}

func (m Model) visibleTable() []protocol.Entry {
	if m.tableView == tableViewCurrent {
		return m.visibleCurrent()
	}
	return m.visibleDiff()
}

func (m Model) visibleEntries(source []protocol.Entry) []protocol.Entry {
	entries := make([]protocol.Entry, 0, len(source))
	for _, entry := range source {
		if m.matchesFilters(entry) {
			entries = append(entries, entry)
		}
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return entryLess(entries[i], entries[j], m.sortMode)
	})

	return entries
}

func (m Model) visibleSearch() []protocol.SearchResult {
	results := make([]protocol.SearchResult, 0, len(m.search))
	for _, result := range m.search {
		if m.matchesFilters(searchResultEntry(result)) {
			results = append(results, result)
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		return searchResultLess(results[i], results[j], m.sortMode)
	})

	return results
}

func entryLess(left protocol.Entry, right protocol.Entry, mode int) bool {
	if mode == sortPagesDesc {
		if left.PageCount != right.PageCount {
			return left.PageCount > right.PageCount
		}
		if left.Size != right.Size {
			return left.Size > right.Size
		}
	}

	if left.Address != right.Address {
		return left.Address < right.Address
	}
	if left.Size != right.Size {
		return left.Size < right.Size
	}
	return left.Tag < right.Tag
}

func searchResultLess(left protocol.SearchResult, right protocol.SearchResult, mode int) bool {
	if mode == sortPagesDesc {
		if left.PageCount != right.PageCount {
			return left.PageCount > right.PageCount
		}
		if left.PoolSize != right.PoolSize {
			return left.PoolSize > right.PoolSize
		}
	}

	if left.Address != right.Address {
		return left.Address < right.Address
	}
	if left.Offset != right.Offset {
		return left.Offset < right.Offset
	}
	return left.Tag < right.Tag
}

func (m Model) exportSearchTarget() string {
	if m.searchText != "" {
		return protocol.SearchTargetName(m.searchStats.Target)
	}
	return protocol.SearchTargetName(m.searchTarget)
}

func (m Model) matchesFilters(entry protocol.Entry) bool {
	if m.filterTag != "" && !strings.Contains(strings.ToUpper(entry.TagString()), strings.ToUpper(m.filterTag)) {
		return false
	}
	if !matchTriState(m.filterAccessible, entry.AccessiblePages > 0 || entry.Flags&protocol.FlagAccessible != 0) {
		return false
	}
	if !matchTriState(m.filterWritable, entry.WritablePages > 0 || entry.Flags&protocol.FlagWritable != 0) {
		return false
	}
	if !matchTriState(m.filterExecutable, entry.ExecutablePages > 0 || entry.Flags&protocol.FlagExecutableAny != 0 || entry.Flags&protocol.FlagExecutable != 0) {
		return false
	}
	return true
}

func filtersText(m Model) string {
	parts := []string{
		"tag=" + valueOrAny(m.filterTag),
		"access=" + triStateText(m.filterAccessible),
		"write=" + triStateText(m.filterWritable),
		"exec=" + triStateText(m.filterExecutable),
	}
	return strings.Join(parts, " ")
}

func valueOrAny(value string) string {
	if value == "" {
		return "any"
	}
	return value
}

func matchTriState(state triState, value bool) bool {
	switch state {
	case triYes:
		return value
	case triNo:
		return !value
	default:
		return true
	}
}

func nextTriState(state triState) triState {
	switch state {
	case triAny:
		return triYes
	case triYes:
		return triNo
	default:
		return triAny
	}
}

func triStateText(state triState) string {
	switch state {
	case triYes:
		return "yes"
	case triNo:
		return "no"
	default:
		return "any"
	}
}

func nextSortMode(value int) int {
	if value == sortAddress {
		return sortPagesDesc
	}
	return sortAddress
}

func sortModeText(value int) string {
	if value == sortPagesDesc {
		return "pages desc"
	}
	return "address asc"
}

func toggleSearchTarget(target uint32) uint32 {
	if target == protocol.SearchTargetDiff {
		return protocol.SearchTargetCurrent
	}
	return protocol.SearchTargetDiff
}

func toggleTableView(value string) string {
	if value == tableViewDiff {
		return tableViewCurrent
	}
	return tableViewDiff
}

func validTriState(value int) triState {
	state := triState(value)
	switch state {
	case triAny, triYes, triNo:
		return state
	default:
		return triAny
	}
}

func validSortMode(value int, legacySizeSort int) int {
	if value == sortPagesDesc {
		return sortPagesDesc
	}
	if legacySizeSort != 0 {
		return sortPagesDesc
	}
	return sortAddress
}

func (m *Model) persistConfig() {
	state := appconfig.State{
		DriverPath:       m.driverPath,
		ExportDir:        m.exportDir,
		SearchTarget:     m.searchTarget,
		FilterTag:        m.filterTag,
		FilterWritable:   int(m.filterWritable),
		FilterExecutable: int(m.filterExecutable),
		FilterAccessible: int(m.filterAccessible),
		SortMode:         m.sortMode,
		TableView:        m.tableView,
		HashMode:         m.hashMode,
		HashSampleBytes:  m.hashSampleBytes,
	}
	if err := appconfig.Save(m.configPath, state); err != nil {
		m.addLog("config save failed: " + friendlyError(err))
	}
}

func searchResultEntry(result protocol.SearchResult) protocol.Entry {
	return protocol.Entry{
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
}

func friendlyError(err error) string {
	if err == nil {
		return ""
	}

	var errno syscall.Errno
	if errors.As(err, &errno) {
		switch errno {
		case syscall.Errno(2):
			return err.Error() + " (not found; the driver path, service, or selected pool entry may no longer exist)"
		case syscall.Errno(5):
			return err.Error() + " (access denied; run the TUI as Administrator)"
		case syscall.Errno(577):
			return err.Error() + " (driver signature is not trusted)"
		case syscall.Errno(1275):
			return err.Error() + " (driver load was blocked; check test-signing, HVCI, and signature policy)"
		}
	}
	return err.Error()
}

func clampSelection(selected int, length int) int {
	if length == 0 {
		return 0
	}
	if selected >= length {
		return length - 1
	}
	if selected < 0 {
		return 0
	}
	return selected
}

func findEntryIndex(entries []protocol.Entry, address uint64) int {
	for index, entry := range entries {
		if addressInEntry(address, entry) {
			return index
		}
	}
	return clampSelection(0, len(entries))
}

func addressInEntry(address uint64, entry protocol.Entry) bool {
	end := entry.Address + entry.Size
	if end < entry.Address {
		end = ^uint64(0)
	}
	return entry.Address == address || (address >= entry.Address && address < end)
}

func addOffset(address uint64, offset uint64) uint64 {
	value := address + offset
	if value < address {
		return ^uint64(0)
	}
	return value
}
