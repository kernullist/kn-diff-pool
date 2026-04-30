package device

import (
	"bytes"
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"

	"kn-diff-pool/kn-pool-diff/internal/protocol"
)

var deviceHandleMu sync.Mutex

type SelfTestResult struct {
	BaselineCount uint32
	DiffCount     uint32
	TestPool      protocol.TestAllocResponse
	Entries       []protocol.Entry
	Search        SearchContentResult
	DumpEntry     protocol.Entry
	DumpOffset    uint64
	DumpData      []byte
}

type SearchContentResult struct {
	Results        []protocol.SearchResult
	TotalMatches   uint32
	Truncated      bool
	EntriesScanned uint32
	EntriesSkipped uint32
	ReadFailures   uint32
	Target         uint32
}

type PTEDetailsResult struct {
	Address       uint64
	PoolSize      uint64
	PageCount     uint32
	ReturnedPages uint32
	Truncated     bool
	Flags         uint32
	Pages         []protocol.PTEPage
}

func RunWithoutOpenHandles(fn func() error) error {
	deviceHandleMu.Lock()
	defer deviceHandleMu.Unlock()

	if fn == nil {
		return nil
	}
	return fn()
}

func Ping() (protocol.PingResponse, error) {
	var response protocol.PingResponse
	returned, err := ioctlOut(
		protocol.IOCTLKnDiffPoolPing,
		(*byte)(unsafe.Pointer(&response)),
		uint32(unsafe.Sizeof(response)))
	if err != nil {
		return protocol.PingResponse{}, err
	}

	outSize := uint32(unsafe.Sizeof(response))
	if returned < outSize {
		return protocol.PingResponse{}, fmt.Errorf("short ping response: got %d bytes, want %d", returned, outSize)
	}
	if response.Magic != protocol.Magic {
		return protocol.PingResponse{}, fmt.Errorf("unexpected driver magic: 0x%08X", response.Magic)
	}
	if response.ProtocolVersion != protocol.ProtocolVersion {
		return protocol.PingResponse{}, fmt.Errorf("protocol mismatch: driver=%d client=%d", response.ProtocolVersion, protocol.ProtocolVersion)
	}

	return response, nil
}

func CreateBaseline() (uint32, error) {
	return countRequest(protocol.IOCTLKnDiffPoolCreateBaseline)
}

func CreateCurrent() (uint32, error) {
	return countRequest(protocol.IOCTLKnDiffPoolCreateCurrent)
}

func DiffCount() (uint32, error) {
	return countRequest(protocol.IOCTLKnDiffPoolGetDiffCount)
}

func DiffEntries() ([]protocol.Entry, uint32, error) {
	count, err := DiffCount()
	if err != nil {
		return nil, 0, err
	}
	return entriesRequest(count, protocol.IOCTLKnDiffPoolGetDiffEntries)
}

func CurrentCount() (uint32, error) {
	return countRequest(protocol.IOCTLKnDiffPoolGetCurrentCount)
}

func CurrentEntries() ([]protocol.Entry, uint32, error) {
	count, err := CurrentCount()
	if err != nil {
		return nil, 0, err
	}
	return entriesRequest(count, protocol.IOCTLKnDiffPoolGetCurrentEntries)
}

func entriesRequest(count uint32, code uint32) ([]protocol.Entry, uint32, error) {
	if count == 0 {
		return nil, 0, nil
	}

	bufferSize := protocol.EntriesHeaderSize + int(count)*protocol.EntrySize
	buffer := make([]byte, bufferSize)

	returned, err := ioctlOut(code, &buffer[0], uint32(len(buffer)))
	if err != nil {
		return nil, 0, err
	}
	if returned < protocol.EntriesHeaderSize {
		return nil, 0, fmt.Errorf("short entries response: got %d bytes", returned)
	}

	header := (*protocol.EntriesResponseHeader)(unsafe.Pointer(&buffer[0]))
	if header.Count == 0 {
		return nil, header.TotalCount, nil
	}
	if header.Count > count {
		return nil, header.TotalCount, fmt.Errorf("entries response count exceeds capacity: got %d, capacity %d", header.Count, count)
	}
	required := protocol.EntriesHeaderSize + int(header.Count)*protocol.EntrySize
	if int(returned) < required {
		return nil, header.TotalCount, fmt.Errorf("short entries response: got %d bytes, want %d", returned, required)
	}

	entriesPtr := (*protocol.Entry)(unsafe.Pointer(&buffer[protocol.EntriesHeaderSize]))
	entriesView := unsafe.Slice(entriesPtr, int(header.Count))
	entries := append([]protocol.Entry(nil), entriesView...)

	return entries, header.TotalCount, nil
}

func ReadContent(address, offset uint64, length uint32) ([]byte, error) {
	if length > protocol.MaxReadLength {
		length = protocol.MaxReadLength
	}

	outputSize := protocol.ReadHeaderSize + int(length)
	bufferSize := max(protocol.ReadRequestSize, outputSize)
	buffer := make([]byte, bufferSize)

	request := (*protocol.ReadRequest)(unsafe.Pointer(&buffer[0]))
	request.Address = address
	request.Offset = offset
	request.Length = length

	returned, err := ioctlBuffered(
		protocol.IOCTLKnDiffPoolReadContent,
		buffer,
		protocol.ReadRequestSize,
		uint32(outputSize))
	if err != nil {
		return nil, fmt.Errorf("read content 0x%016X+0x%X length %d: %w", address, offset, length, err)
	}
	if returned < protocol.ReadHeaderSize {
		return nil, fmt.Errorf("short read response: got %d bytes", returned)
	}

	header := (*protocol.ReadResponseHeader)(unsafe.Pointer(&buffer[0]))
	if header.Address != address || header.Offset != offset {
		return nil, fmt.Errorf(
			"read response mismatch: got 0x%016X+0x%X, want 0x%016X+0x%X",
			header.Address,
			header.Offset,
			address,
			offset)
	}
	if header.BytesRead == 0 {
		return nil, nil
	}
	if int(header.BytesRead) > len(buffer)-protocol.ReadHeaderSize {
		return nil, fmt.Errorf("invalid read response size: %d", header.BytesRead)
	}
	required := protocol.ReadHeaderSize + int(header.BytesRead)
	if int(returned) < required {
		return nil, fmt.Errorf("short read response: got %d bytes, want %d", returned, required)
	}

	data := append([]byte(nil), buffer[protocol.ReadHeaderSize:protocol.ReadHeaderSize+int(header.BytesRead)]...)
	return data, nil
}

func SearchContent(pattern []byte, maxResults uint32, target uint32) (SearchContentResult, error) {
	if len(pattern) == 0 {
		return SearchContentResult{}, fmt.Errorf("empty search pattern")
	}
	if len(pattern) > protocol.MaxSearchPattern {
		return SearchContentResult{}, fmt.Errorf("search pattern too long: %d bytes", len(pattern))
	}
	if maxResults == 0 || maxResults > protocol.MaxSearchResults {
		maxResults = protocol.MaxSearchResults
	}
	if target != protocol.SearchTargetDiff && target != protocol.SearchTargetCurrent {
		return SearchContentResult{}, fmt.Errorf("invalid search target: %d", target)
	}

	outputSize := protocol.SearchHeaderSize + int(maxResults)*protocol.SearchResultSize
	bufferSize := max(protocol.SearchRequestSize, outputSize)
	buffer := make([]byte, bufferSize)

	request := (*protocol.SearchRequest)(unsafe.Pointer(&buffer[0]))
	request.PatternLength = uint32(len(pattern))
	request.MaxResults = maxResults
	request.Target = target
	copy(request.Pattern[:], pattern)

	returned, err := ioctlBuffered(
		protocol.IOCTLKnDiffPoolSearchContent,
		buffer,
		protocol.SearchRequestSize,
		uint32(outputSize))
	if err != nil {
		return SearchContentResult{}, fmt.Errorf("search %s content: %w", protocol.SearchTargetName(target), err)
	}
	if returned < protocol.SearchHeaderSize {
		return SearchContentResult{}, fmt.Errorf("short search response: got %d bytes", returned)
	}

	header := (*protocol.SearchResponseHeader)(unsafe.Pointer(&buffer[0]))
	if header.Target != target {
		return SearchContentResult{}, fmt.Errorf("search response target mismatch: got %s, want %s", protocol.SearchTargetName(header.Target), protocol.SearchTargetName(target))
	}
	result := SearchContentResult{
		TotalMatches:   header.TotalMatches,
		Truncated:      header.Truncated != 0,
		EntriesScanned: header.EntriesScanned,
		EntriesSkipped: header.EntriesSkipped,
		ReadFailures:   header.ReadFailures,
		Target:         header.Target,
	}
	if header.Count == 0 {
		return result, nil
	}
	if header.Count > maxResults {
		return result, fmt.Errorf("search response count exceeds capacity: got %d, capacity %d", header.Count, maxResults)
	}
	required := protocol.SearchHeaderSize + int(header.Count)*protocol.SearchResultSize
	if int(returned) < required {
		return result, fmt.Errorf("short search response: got %d bytes, want %d", returned, required)
	}

	resultsPtr := (*protocol.SearchResult)(unsafe.Pointer(&buffer[protocol.SearchHeaderSize]))
	resultsView := unsafe.Slice(resultsPtr, int(header.Count))
	result.Results = append([]protocol.SearchResult(nil), resultsView...)

	return result, nil
}

func ClearSnapshots() (uint32, error) {
	return countRequest(protocol.IOCTLKnDiffPoolClearSnapshots)
}

func AllocateTestPool() (protocol.TestAllocResponse, error) {
	bufferSize := max(protocol.TestAllocRequestSize, protocol.TestAllocResponseSize)
	buffer := make([]byte, bufferSize)

	request := (*protocol.TestAllocRequest)(unsafe.Pointer(&buffer[0]))
	request.Size = protocol.DefaultTestSize
	request.Tag = protocol.PoolTag("KDTP")
	request.FillLength = uint32(len(protocol.TestPayload))
	copy(request.Fill[:], []byte(protocol.TestPayload))

	returned, err := ioctlBuffered(
		protocol.IOCTLKnDiffPoolAllocTestPool,
		buffer,
		protocol.TestAllocRequestSize,
		protocol.TestAllocResponseSize)
	if err != nil {
		return protocol.TestAllocResponse{}, err
	}
	if returned < protocol.TestAllocResponseSize {
		return protocol.TestAllocResponse{}, fmt.Errorf("short test allocation response: got %d bytes", returned)
	}

	response := *(*protocol.TestAllocResponse)(unsafe.Pointer(&buffer[0]))
	return response, nil
}

func FreeTestPools() (uint32, error) {
	return countRequest(protocol.IOCTLKnDiffPoolFreeTestPools)
}

func PTEDetails(address uint64, maxPages uint32) (PTEDetailsResult, error) {
	if maxPages == 0 || maxPages > protocol.MaxPTEDetailPages {
		maxPages = protocol.MaxPTEDetailPages
	}

	outputSize := protocol.PTEHeaderSize + int(maxPages)*protocol.PTEPageSize
	bufferSize := max(protocol.PTERequestSize, outputSize)
	buffer := make([]byte, bufferSize)

	request := (*protocol.PTERequest)(unsafe.Pointer(&buffer[0]))
	request.Address = address
	request.MaxPages = maxPages

	returned, err := ioctlBuffered(
		protocol.IOCTLKnDiffPoolGetPTEDetails,
		buffer,
		protocol.PTERequestSize,
		uint32(outputSize))
	if err != nil {
		return PTEDetailsResult{}, fmt.Errorf("pte details 0x%016X: %w", address, err)
	}
	if returned < protocol.PTEHeaderSize {
		return PTEDetailsResult{}, fmt.Errorf("short pte response: got %d bytes", returned)
	}

	header := (*protocol.PTEResponseHeader)(unsafe.Pointer(&buffer[0]))
	if !addressInEntry(address, protocol.Entry{Address: header.Address, Size: header.PoolSize}) {
		return PTEDetailsResult{}, fmt.Errorf(
			"pte response range mismatch: got 0x%016X+0x%X, want address 0x%016X",
			header.Address,
			header.PoolSize,
			address)
	}
	if header.ReturnedPages > maxPages {
		return PTEDetailsResult{}, fmt.Errorf("pte response pages exceed capacity: got %d, capacity %d", header.ReturnedPages, maxPages)
	}
	required := protocol.PTEHeaderSize + int(header.ReturnedPages)*protocol.PTEPageSize
	if int(returned) < required {
		return PTEDetailsResult{}, fmt.Errorf("short pte response: got %d bytes, want %d", returned, required)
	}

	pagesPtr := (*protocol.PTEPage)(unsafe.Pointer(&buffer[protocol.PTEHeaderSize]))
	pagesView := unsafe.Slice(pagesPtr, int(header.ReturnedPages))

	return PTEDetailsResult{
		Address:       header.Address,
		PoolSize:      header.PoolSize,
		PageCount:     header.PageCount,
		ReturnedPages: header.ReturnedPages,
		Truncated:     header.Truncated != 0,
		Flags:         header.Flags,
		Pages:         append([]protocol.PTEPage(nil), pagesView...),
	}, nil
}

func SetHashPolicy(mode uint32, sampleBytes uint64) error {
	if mode != protocol.HashModeSample && mode != protocol.HashModeFull {
		return fmt.Errorf("invalid hash mode: %d", mode)
	}

	buffer := make([]byte, protocol.HashPolicySize)
	request := (*protocol.HashPolicyRequest)(unsafe.Pointer(&buffer[0]))
	request.Mode = mode
	request.SampleBytes = sampleBytes

	_, err := ioctlBuffered(
		protocol.IOCTLKnDiffPoolSetHashPolicy,
		buffer,
		protocol.HashPolicySize,
		0)
	if err != nil {
		return fmt.Errorf("set hash policy %s: %w", protocol.HashModeName(mode), err)
	}
	return nil
}

func RunSelfTest() (SelfTestResult, error) {
	var result SelfTestResult
	allocated := false
	succeeded := false

	defer func() {
		if allocated && !succeeded {
			_, _ = FreeTestPools()
			_, _ = ClearSnapshots()
		}
	}()

	if _, err := Ping(); err != nil {
		return result, fmt.Errorf("ping: %w", err)
	}
	if _, err := FreeTestPools(); err != nil {
		return result, fmt.Errorf("free previous test pools: %w", err)
	}
	if _, err := ClearSnapshots(); err != nil {
		return result, fmt.Errorf("clear snapshots: %w", err)
	}

	baselineCount, err := CreateBaseline()
	if err != nil {
		return result, fmt.Errorf("create baseline: %w", err)
	}
	result.BaselineCount = baselineCount

	testPool, err := AllocateTestPool()
	if err != nil {
		return result, fmt.Errorf("allocate test pool: %w", err)
	}
	allocated = true
	result.TestPool = testPool

	diffCount, err := CreateCurrent()
	if err != nil {
		return result, fmt.Errorf("create current snapshot: %w", err)
	}
	result.DiffCount = diffCount

	entries, total, err := DiffEntries()
	if err != nil {
		return result, fmt.Errorf("load diff entries: %w", err)
	}
	result.Entries = entries
	result.DiffCount = total

	if !containsTestPool(entries, testPool.Address) {
		return result, fmt.Errorf("test pool 0x%016X was not present in diff entries", testPool.Address)
	}

	search, err := SearchContent([]byte(protocol.TestPayload), 8, protocol.SearchTargetDiff)
	if err != nil {
		return result, fmt.Errorf("search test payload: %w", err)
	}
	result.Search = search

	if len(search.Results) == 0 {
		return result, fmt.Errorf("test payload was not found in diff pool contents")
	}

	dumpData, err := ReadContent(search.Results[0].Address, search.Results[0].Offset, uint32(len(protocol.TestPayload)))
	if err != nil {
		return result, fmt.Errorf("read matched payload: %w", err)
	}
	if !bytes.HasPrefix(dumpData, []byte(protocol.TestPayload)) {
		return result, fmt.Errorf("dump data did not match test payload")
	}

	result.DumpEntry = searchResultAsEntry(search.Results[0])
	result.DumpOffset = search.Results[0].Offset
	result.DumpData = dumpData
	succeeded = true
	return result, nil
}

func countRequest(code uint32) (uint32, error) {
	var response protocol.CountResponse
	returned, err := ioctlOut(
		code,
		(*byte)(unsafe.Pointer(&response)),
		uint32(unsafe.Sizeof(response)))
	if err != nil {
		return 0, err
	}
	if returned < uint32(unsafe.Sizeof(response)) {
		return 0, fmt.Errorf("short count response: got %d bytes", returned)
	}

	return response.Count, nil
}

func ioctlOut(code uint32, outBuffer *byte, outBufferSize uint32) (uint32, error) {
	return ioctlRaw(code, nil, 0, outBuffer, outBufferSize)
}

func ioctlBuffered(code uint32, buffer []byte, inputSize uint32, outputSize uint32) (uint32, error) {
	if len(buffer) == 0 {
		return 0, fmt.Errorf("empty ioctl buffer")
	}
	return ioctlRaw(code, &buffer[0], inputSize, &buffer[0], outputSize)
}

func ioctlRaw(code uint32, inBuffer *byte, inBufferSize uint32, outBuffer *byte, outBufferSize uint32) (uint32, error) {
	deviceHandleMu.Lock()
	defer deviceHandleMu.Unlock()

	path, err := windows.UTF16PtrFromString(protocol.DevicePath)
	if err != nil {
		return 0, err
	}

	handle, err := windows.CreateFile(
		path,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", protocol.DevicePath, err)
	}
	defer windows.CloseHandle(handle)

	var returned uint32
	err = windows.DeviceIoControl(
		handle,
		code,
		inBuffer,
		inBufferSize,
		outBuffer,
		outBufferSize,
		&returned,
		nil)
	if err != nil {
		return 0, fmt.Errorf("ioctl 0x%08X: %w", code, err)
	}

	return returned, nil
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func containsTestPool(entries []protocol.Entry, address uint64) bool {
	for _, entry := range entries {
		if addressInEntry(address, entry) {
			return true
		}
	}
	return false
}

func addressInEntry(address uint64, entry protocol.Entry) bool {
	end := entry.Address + entry.Size
	if end < entry.Address {
		end = ^uint64(0)
	}
	return entry.Address == address || (address >= entry.Address && address < end)
}

func searchResultAsEntry(result protocol.SearchResult) protocol.Entry {
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
