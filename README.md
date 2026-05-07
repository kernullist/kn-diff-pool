# Kn Diff Pool

Windows kernel Big Pool snapshot/diff tool with a kernel-mode driver and a Go TUI.

Kn Diff Pool captures a baseline snapshot of Windows kernel Big Pool allocations, captures another snapshot later, and shows allocations that are newly visible compared with the baseline. It can inspect pool metadata, dump readable bytes, search pool content, view page-level PTE summaries, filter/sort results, and export analysis data.

> Status: research/prototype tooling. Use it in a lab VM or controlled test machine first.

## What This Project Does

- Captures a baseline Big Pool snapshot.
- Captures a current Big Pool snapshot and computes new Big Pool allocations.
- Shows each result with address, size, tag, page accessibility/write/execute summary, content hash, and properties.
- Dumps readable bytes from a selected pool allocation.
- Searches ASCII, hex, or UTF-16LE patterns in either the diff result or the current snapshot.
- Reports search statistics, including skipped/inaccessible entries and read failures.
- Shows page-level PTE details for selected allocations.
- Filters by tag, accessibility, writable pages, and executable pages.
- Sorts by address ascending or page count descending.
- Exports diff/current/search results as JSON and CSV.
- Saves pool dumps as `.bin` plus JSON metadata.
- Installs/starts and stops/deletes the driver through the Service Control Manager, without an INF install flow.

## Why Big Pool

Some malware and game cheats place executable payloads or support code in kernel pool memory. When that code is large enough, it often lands in Big Pool allocations rather than small pool allocations.

In an analysis sandbox, this makes Big Pool diffing useful: capture a baseline Big Pool snapshot before running the malware or game cheat, run the sample, then capture another snapshot and inspect the diff. Newly visible Big Pool allocations can narrow the search space for hidden kernel-resident code and make subsequent dump, search, and PTE analysis more direct.

## Important Scope and Accuracy Notes

This tool is a **Big Pool snapshot/diff** tool, not a complete kernel pool allocator tracer.

- Big Pool enumeration is based on `SystemBigPoolInformation` through `ZwQuerySystemInformation`. This is not a fully documented stable pool enumeration contract, and it only covers Big Pool style allocations. Small pool allocations are not expected to appear.
- Pool memory is live. An allocation can be freed or reused between enumeration, hashing, search, and dump. The driver uses safe copy paths to avoid crashing, but results are best-effort.
- PTE analysis is also best-effort. It walks page tables from the current CR3 and summarizes page flags. KVA shadow, VBS/HVCI, session mappings, and OS layout changes can affect W/X classification.
- Dump/search can fail for individual entries if pages are no longer readable. The TUI reports skipped/read-failure counts where available.
- The device object is created with an admin/system-only security descriptor. Run the TUI elevated for normal operation.

## Repository Layout

```text
src/
  shared/
    kn_diff_protocol.h     Shared IOCTL protocol and structs for the driver

  kn-diff/
    kn-diff.sln            Visual Studio / WDK solution
    kn-diff/
      kn-diff.cpp          Kernel driver implementation

  user/
    kn-pool-diff/
      cmd/kn-pool-diff/    TUI entry point
      internal/            Go packages for device IOCTLs, SCM, TUI, export, config
```

## Requirements

- Windows 10/11 x64 test machine.
- Visual Studio 2022 with C++ workload.
- Windows Driver Kit (WDK) for Windows 10/11.
- Go 1.21 or newer.
- Administrator shell for driver installation, start, stop, and delete.
- Test signing enabled for unsigned/test-signed builds.

Enable test signing on a test machine:

```powershell
bcdedit /set testsigning on
shutdown /r /t 0
```

For production use, replace test signing with a proper driver signing flow.

## Build

From the repository root:

```powershell
msbuild .\src\kn-diff\kn-diff.sln /p:Configuration=Release /p:Platform=x64 /m
```

Build the Go TUI:

```powershell
cd .\src\user\kn-pool-diff
go build -o kn-pool-diff.exe .\cmd\kn-pool-diff
```

Place the driver next to the TUI executable for the default runtime layout:

```powershell
Copy-Item ..\..\kn-diff\x64\Release\kn-diff.sys .\kn-diff.sys -Force
```

Run tests:

```powershell
cd .\src\user\kn-pool-diff
go test ./...
go vet ./...
```

## Driver Path Resolution

The TUI installs the driver through SCM. It does not use an INF-based install path.

The user-mode tool resolves the driver path in this order:

1. `KN_DIFF_DRIVER_PATH` environment variable.
2. `kn-diff.sys` in the same directory as `kn-pool-diff.exe`.
3. Fallback: `kn-diff.sys` under the current working directory if the executable path cannot be resolved.

If you build `Release`, either set `KN_DIFF_DRIVER_PATH` or copy the built driver next to the TUI executable.

Example from the repository root:

```powershell
$env:KN_DIFF_DRIVER_PATH = (Resolve-Path .\src\kn-diff\x64\Release\kn-diff.sys)
.\src\user\kn-pool-diff\kn-pool-diff.exe
```

## Running the TUI

Run from an elevated PowerShell when you want to install/start/stop/delete the driver:

```powershell
.\src\user\kn-pool-diff\kn-pool-diff.exe
```

The TUI prevents duplicate instances with a named mutex.

Exit behavior:

- Elevated: exit closes device handles and attempts to stop/delete the driver service.
- Not elevated: exit still works, but driver unload/delete is skipped.

## TUI Workflow

Typical flow:

1. Press `s` to install and start the driver.
2. Press `b` to capture the baseline Big Pool snapshot.
3. Perform the activity you want to compare.
4. Press `c` to capture the current snapshot and compute the diff.
5. Inspect, filter, search, dump, save selected pools, or export results.
6. Press `t` to unload/delete the driver, or `q` to exit.

Capturing `b` again refreshes the baseline. Existing current/diff/search/dump/page-detail state is cleared because it belongs to the previous baseline.

## TUI Shortcuts

Only currently valid shortcuts are shown at the bottom of the TUI.

| Key | Available When | Action |
| --- | --- | --- |
| `s` | Driver is not running | Install service and load driver |
| `t` | Driver is running | Stop driver and delete service |
| `q` | Always | Quit |
| `esc` | Detail/input/search/dump state is open | Go back one level |
| `b` | Driver is running | Capture or refresh baseline |
| `c` | Baseline exists | Capture current snapshot and compute diff |
| `l` | Diff exists | Reload diff entries |
| `up/down` or `k/j` | Table is visible | Move selected pool row |
| `pgup/pgdn` | Table/body can scroll | Scroll the body |
| `home/end` | Table/body can scroll | Jump to top/bottom |
| `enter` or `x` | Pool row is selected | Dump readable bytes from selected pool |
| `[` / `]` | Dump is visible | Previous/next dump page |
| `p` | Pool row is selected | Save readable bytes from selected pool to a file |
| `d` | Pool row is selected | Show page-level PTE details |
| `tab` | Diff exists | Toggle table between diff and current snapshot |
| `/` | Diff exists | Search ASCII text |
| `h` | Diff exists | Search hex bytes |
| `u` | Diff exists | Search UTF-16LE text |
| `m` | Diff exists | Toggle search target: diff/current |
| `g` | Search result selected | Dump the selected search hit |
| `n` | Search results visible | Move to next search result |
| `f` | Diff exists | Set/clear tag filter |
| `w` | Diff exists | Cycle writable filter: any/yes/no |
| `e` | Diff exists | Cycle executable filter: any/yes/no |
| `a` | Diff exists | Cycle accessibility filter: any/yes/no |
| `o` | Diff exists | Toggle sort: address ascending/pages descending |
| `r` | Diff exists | Export JSON and CSV |
| `v` | Dump is visible | Save dump bytes and metadata |

## Output and Export

By default, exports are written under an `exports` directory next to `kn-pool-diff.exe`.

- `r` writes both JSON and CSV analysis files.
- `v` writes a dump `.bin` file and a JSON metadata file.
- `p` writes a selected-pool `.bin` file and JSON metadata. It reads the pool in chunks and caps a single save at 64 MiB; truncated saves are marked in metadata.
- To save from the current snapshot instead of the diff table, press `tab` after `c`, select the desired current pool row, then press `p`.
- JSON includes diff entries, current entries, search results, search target, search text/kind, table view, and hash mode.
- CSV includes diff/current/search rows with address, offset, size, tag, flags, page counts, content hash, hashed byte count, and snapshot ID.

## Kernel Driver Notes

Driver/service values:

- Service name: `kn-diff`
- Display name: `KN Diff Big Pool Driver`
- Win32 device path: `\\.\KnDiffPool`
- Kernel device name: `\Device\KnDiffPool`

The driver uses `IoCreateDeviceSecure` with `SDDL_DEVOBJ_SYS_ALL_ADM_ALL`, so only SYSTEM and Administrators should be able to open the device.

Implemented IOCTL families include:

- Ping/version check.
- Baseline/current snapshot capture.
- Diff/current entry retrieval.
- Pool content read.
- Pool content search.
- PTE details.
- Hash policy.
- Internal test allocation helpers still exist in the protocol/driver, but the TUI intentionally does not expose debug commands.

## Development Checklist

Before pushing changes:

```powershell
cd .\src\user\kn-pool-diff
go test ./...
go vet ./...
go build -o kn-pool-diff.exe .\cmd\kn-pool-diff
cd ..\..\..
msbuild .\src\kn-diff\kn-diff.sln /p:Configuration=Release /p:Platform=x64 /m
```

Build artifacts such as `x64/`, `.vs/`, `*.exe`, `*.sys`, `*.pdb`, and export output are ignored by git.

## Safety

This project reads kernel memory through a custom kernel driver. Treat it as sensitive tooling:

- Use a dedicated test VM first.
- Do not run on production systems until the driver has been reviewed, signed, and validated for the target OS builds.
- Expect OS-specific behavior around Big Pool enumeration and PTE interpretation.
- Do not assume results are forensic-grade. They are useful for investigation and debugging, but live kernel memory can change under the tool.

---