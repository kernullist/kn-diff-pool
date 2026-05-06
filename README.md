# KN Diff Pool

[한국어 README](README_kor.md)

Windows kernel Big Pool snapshot/diff tool with a kernel-mode driver and a Go TUI.

KN Diff Pool captures a baseline snapshot of Windows kernel Big Pool allocations, captures another snapshot later, and shows allocations that are newly visible compared with the baseline. It can inspect pool metadata, dump readable bytes, search pool content, view page-level PTE summaries, filter/sort results, and export analysis data.

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

## Important Scope and Accuracy Notes

This tool is a **Big Pool snapshot/diff** tool, not a complete kernel pool allocator tracer.

- Big Pool enumeration is based on `SystemBigPoolInformation` through `ZwQuerySystemInformation`. This is not a fully documented stable pool enumeration contract, and it only covers Big Pool style allocations. Small pool allocations are not expected to appear.
- Pool memory is live. An allocation can be freed or reused between enumeration, hashing, search, and dump. The driver uses safe copy paths to avoid crashing, but results are best-effort.
- PTE analysis is also best-effort. It walks page tables from the current CR3 and summarizes page flags. KVA shadow, VBS/HVCI, session mappings, and OS layout changes can affect W/X classification.
- Dump/search can fail for individual entries if pages are no longer readable. The TUI reports skipped/read-failure counts where available.
- The device object is created with an admin/system-only security descriptor. Run the TUI elevated for normal operation.

## Repository Layout

```text
ref/
  pool_finder.cpp          Original Big Pool scanning POC
  pte.cpp                  Original PTE inspection POC

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
5. Inspect, filter, search, dump, or export results.
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

# KN Diff Pool 한국어 설명

Windows 커널 Big Pool 스냅샷/비교 도구입니다. 커널 드라이버와 Go 기반 TUI로 구성되어 있습니다.

KN Diff Pool은 기준점이 되는 Windows 커널 Big Pool 스냅샷을 캡쳐한 뒤, 원하는 시점에 다시 스냅샷을 캡쳐하여 기준점 이후 새로 보이는 Big Pool allocation을 보여줍니다. 각 Pool의 메타데이터, 읽을 수 있는 내용 덤프, 내용 검색, 페이지별 PTE 요약, 필터/정렬, JSON/CSV export를 지원합니다.

> 현재 상태: 연구/프로토타입 도구입니다. 먼저 랩 VM이나 통제된 테스트 머신에서 사용하세요.

## 주요 기능

- 기준 Big Pool 스냅샷 캡쳐.
- 현재 Big Pool 스냅샷 캡쳐 및 기준점 대비 신규 Big Pool allocation 계산.
- 주소, 크기, tag, 페이지 접근성/쓰기/실행 요약, content hash, 속성 표시.
- 선택한 Pool의 읽을 수 있는 바이트 덤프.
- diff 결과 또는 current 전체 스냅샷에서 ASCII, hex, UTF-16LE 패턴 검색.
- 검색 중 건너뛴/inaccessible entry 및 read failure 통계 표시.
- 선택한 Pool의 페이지별 PTE 상세 표시.
- tag, accessibility, writable, executable 필터.
- 주소 오름차순 또는 페이지 수 내림차순 정렬.
- diff/current/search 결과 JSON 및 CSV export.
- Pool dump를 `.bin` 파일과 JSON metadata로 저장.
- INF 설치가 아니라 Service Control Manager를 통해 드라이버 설치/시작/중지/삭제.

## 범위와 정확도 주의사항

이 도구는 **Big Pool snapshot/diff** 도구입니다. 모든 커널 pool allocation을 추적하는 도구가 아닙니다.

- Big Pool 열거는 `ZwQuerySystemInformation(SystemBigPoolInformation)` 기반입니다. 완전히 문서화된 안정 API가 아니며, Big Pool 중심 정보만 제공합니다. 작은 pool allocation은 보이지 않을 수 있습니다.
- 커널 Pool은 계속 변합니다. 주소 목록을 얻은 뒤 hash/search/dump를 수행하는 사이 allocation이 free/reuse될 수 있습니다. 드라이버는 safe copy 경로를 사용해 bugcheck를 피하지만 결과는 best-effort입니다.
- PTE 분석도 best-effort입니다. 현재 CR3 기준으로 page table을 걸어 W/X 등을 요약합니다. KVA shadow, VBS/HVCI, session mapping, Windows 버전 변화에 따라 결과가 달라질 수 있습니다.
- dump/search는 개별 entry가 더 이상 읽을 수 없으면 실패하거나 건너뜁니다. 가능한 경우 TUI에서 skipped/read failure 통계를 보여줍니다.
- device object는 관리자/SYSTEM 전용 보안 설정으로 생성됩니다. 정상 사용은 관리자 권한 TUI 실행을 기준으로 합니다.

## 디렉터리 구조

```text
ref/
  pool_finder.cpp          초기 Big Pool 스캔 POC
  pte.cpp                  초기 PTE 확인 POC

src/
  shared/
    kn_diff_protocol.h     드라이버/유저모드 공유 IOCTL 프로토콜

  kn-diff/
    kn-diff.sln            Visual Studio / WDK 솔루션
    kn-diff/
      kn-diff.cpp          커널 드라이버 구현

  user/
    kn-pool-diff/
      cmd/kn-pool-diff/    TUI 진입점
      internal/            device IOCTL, SCM, TUI, export, config 패키지
```

## 필요 환경

- Windows 10/11 x64 테스트 머신.
- Visual Studio 2022 C++ workload.
- Windows Driver Kit (WDK).
- Go 1.21 이상.
- 드라이버 설치/시작/중지/삭제를 위한 관리자 권한 shell.
- unsigned/test-signed 빌드를 위한 테스트서명 활성화.

테스트 머신에서 테스트서명을 켜는 예:

```powershell
bcdedit /set testsigning on
shutdown /r /t 0
```

제품화 단계에서는 테스트서명이 아니라 정식 드라이버 서명 절차를 사용해야 합니다.

## 빌드

repo root에서 드라이버 빌드:

```powershell
msbuild .\src\kn-diff\kn-diff.sln /p:Configuration=Release /p:Platform=x64 /m
```

Go TUI 빌드:

```powershell
cd .\src\user\kn-pool-diff
go build -o kn-pool-diff.exe .\cmd\kn-pool-diff
```

기본 실행 배치에서는 드라이버를 TUI 실행 파일 옆에 둡니다:

```powershell
Copy-Item ..\..\kn-diff\x64\Release\kn-diff.sys .\kn-diff.sys -Force
```

테스트:

```powershell
cd .\src\user\kn-pool-diff
go test ./...
go vet ./...
```

## 드라이버 경로 해석

TUI는 SCM을 통해 드라이버 서비스를 설치합니다. INF 기반 설치는 사용하지 않습니다.

유저모드 도구는 드라이버 경로를 다음 순서로 찾습니다.

1. `KN_DIFF_DRIVER_PATH` 환경 변수.
2. `kn-pool-diff.exe`와 같은 디렉터리에 있는 `kn-diff.sys`.
3. 실행 파일 경로를 확인할 수 없을 때만 현재 작업 디렉터리 아래 `kn-diff.sys`.

`Release`로 빌드했다면 `KN_DIFF_DRIVER_PATH`를 지정하거나, 빌드된 `kn-diff.sys`를 TUI 실행 파일 옆에 복사하세요.

repo root 기준 예:

```powershell
$env:KN_DIFF_DRIVER_PATH = (Resolve-Path .\src\kn-diff\x64\Release\kn-diff.sys)
.\src\user\kn-pool-diff\kn-pool-diff.exe
```

## TUI 실행

드라이버 설치/시작/중지/삭제를 하려면 관리자 PowerShell에서 실행하세요.

```powershell
.\src\user\kn-pool-diff\kn-pool-diff.exe
```

TUI는 named mutex로 중복 실행을 막습니다.

종료 동작:

- 관리자 권한: device handle을 닫고 드라이버 stop/delete를 시도합니다.
- 비관리자 권한: 종료는 정상 동작하지만 드라이버 unload/delete는 건너뜁니다.

## 기본 사용 흐름

1. `s`를 눌러 드라이버 서비스를 설치하고 로드합니다.
2. `b`를 눌러 baseline Big Pool 스냅샷을 캡쳐합니다.
3. 비교하고 싶은 동작을 수행합니다.
4. `c`를 눌러 current 스냅샷을 캡쳐하고 diff를 계산합니다.
5. 결과를 확인하고, 필터/검색/덤프/export를 수행합니다.
6. `t`로 드라이버를 unload/delete하거나, `q`로 종료합니다.

baseline 캡쳐 후 다시 `b`를 누르면 baseline이 최신 기준점으로 갱신됩니다. 이전 baseline에 속한 current/diff/search/dump/page detail 상태는 초기화됩니다.

## TUI 단축키

현재 사용할 수 있는 단축키만 TUI 하단에 표시됩니다.

| 키 | 사용 가능 시점 | 동작 |
| --- | --- | --- |
| `s` | 드라이버가 실행 중이 아닐 때 | 서비스 설치 및 드라이버 로드 |
| `t` | 드라이버 실행 중 | 드라이버 중지 및 서비스 삭제 |
| `q` | 항상 | 종료 |
| `esc` | detail/input/search/dump 상태가 열려 있을 때 | 이전 단계로 돌아가기 |
| `b` | 드라이버 실행 중 | baseline 캡쳐 또는 갱신 |
| `c` | baseline 존재 | current 캡쳐 및 diff 계산 |
| `l` | diff 존재 | diff entry 다시 로드 |
| `up/down` 또는 `k/j` | table 표시 중 | 선택 row 이동 |
| `pgup/pgdn` | 스크롤 가능 | body 스크롤 |
| `home/end` | 스크롤 가능 | 맨 위/아래로 이동 |
| `enter` 또는 `x` | Pool row 선택됨 | 선택한 Pool bytes dump |
| `[` / `]` | dump 표시 중 | 이전/다음 dump page |
| `d` | Pool row 선택됨 | 페이지별 PTE detail 표시 |
| `tab` | diff 존재 | table을 diff/current로 전환 |
| `/` | diff 존재 | ASCII 검색 |
| `h` | diff 존재 | hex bytes 검색 |
| `u` | diff 존재 | UTF-16LE 검색 |
| `m` | diff 존재 | 검색 대상 diff/current 전환 |
| `g` | search result 선택됨 | 선택한 검색 hit dump |
| `n` | search result 표시 중 | 다음 검색 result 선택 |
| `f` | diff 존재 | tag filter 설정/해제 |
| `w` | diff 존재 | writable filter any/yes/no 순환 |
| `e` | diff 존재 | executable filter any/yes/no 순환 |
| `a` | diff 존재 | accessibility filter any/yes/no 순환 |
| `o` | diff 존재 | 정렬 전환: address asc/pages desc |
| `r` | diff 존재 | JSON 및 CSV export |
| `v` | dump 표시 중 | dump bytes 및 metadata 저장 |

## Export 결과

기본 export 위치는 `kn-pool-diff.exe`와 같은 디렉터리 아래의 `exports` 폴더입니다.

- `r`: JSON 및 CSV 분석 파일을 모두 저장합니다.
- `v`: dump `.bin` 파일과 JSON metadata를 저장합니다.
- JSON에는 diff entries, current entries, search results, search target, search text/kind, table view, hash mode가 포함됩니다.
- CSV에는 diff/current/search row와 address, offset, size, tag, flags, page counts, content hash, hashed byte count, snapshot ID가 포함됩니다.

## 커널 드라이버 정보

드라이버/서비스 값:

- Service name: `kn-diff`
- Display name: `KN Diff Big Pool Driver`
- Win32 device path: `\\.\KnDiffPool`
- Kernel device name: `\Device\KnDiffPool`

드라이버는 `IoCreateDeviceSecure`와 `SDDL_DEVOBJ_SYS_ALL_ADM_ALL`을 사용하므로 SYSTEM과 Administrators만 device를 열 수 있어야 합니다.

구현된 IOCTL 계열:

- Ping/version check.
- Baseline/current snapshot capture.
- Diff/current entry retrieval.
- Pool content read.
- Pool content search.
- PTE details.
- Hash policy.
- 내부 테스트 allocation helper는 protocol/driver에 남아 있지만, TUI에서는 의도적으로 debug command를 노출하지 않습니다.

## 개발 체크리스트

변경사항 push 전 확인:

```powershell
cd .\src\user\kn-pool-diff
go test ./...
go vet ./...
go build -o kn-pool-diff.exe .\cmd\kn-pool-diff
cd ..\..\..
msbuild .\src\kn-diff\kn-diff.sln /p:Configuration=Release /p:Platform=x64 /m
```

`x64/`, `.vs/`, `*.exe`, `*.sys`, `*.pdb`, export 결과 등 빌드 산출물은 git에서 제외됩니다.

## 안전 주의

이 프로젝트는 커스텀 커널 드라이버로 커널 메모리를 읽습니다. 민감한 도구로 다루세요.

- 먼저 전용 테스트 VM에서 사용하세요.
- 대상 OS 빌드에서 충분히 검증하고 정식 서명하기 전에는 운영 환경에서 사용하지 마세요.
- Big Pool enumeration과 PTE 해석은 OS 버전별 차이를 가질 수 있습니다.
- 결과를 forensic-grade로 가정하지 마세요. 분석/디버깅에는 유용하지만, live kernel memory는 도구가 읽는 동안에도 계속 변할 수 있습니다.
