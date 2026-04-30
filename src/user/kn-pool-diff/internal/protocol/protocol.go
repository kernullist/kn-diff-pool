package protocol

const (
	DevicePath      = `\\.\KnDiffPool`
	ServiceName     = "kn-diff"
	ServiceDisplay  = "KN Diff Big Pool Driver"
	ProtocolVersion = uint32(3)
	Magic           = uint32(0x50444E4B)
	TestPayload     = "KN_DIFF_POOL_TEST_PAYLOAD::baseline-to-current-diff::"
)

const (
	fileDeviceKnDiffPool = uint32(0x8000)
	methodBuffered       = uint32(0)
	fileReadData         = uint32(0x0001)
	fileWriteData        = uint32(0x0002)
)

const IOCTLKnDiffPoolPing = (fileDeviceKnDiffPool << 16) |
	((fileReadData | fileWriteData) << 14) |
	(uint32(0x800) << 2) |
	methodBuffered
const IOCTLKnDiffPoolCreateBaseline = (fileDeviceKnDiffPool << 16) |
	((fileReadData | fileWriteData) << 14) |
	(uint32(0x801) << 2) |
	methodBuffered
const IOCTLKnDiffPoolCreateCurrent = (fileDeviceKnDiffPool << 16) |
	((fileReadData | fileWriteData) << 14) |
	(uint32(0x802) << 2) |
	methodBuffered
const IOCTLKnDiffPoolGetDiffCount = (fileDeviceKnDiffPool << 16) |
	((fileReadData | fileWriteData) << 14) |
	(uint32(0x803) << 2) |
	methodBuffered
const IOCTLKnDiffPoolGetDiffEntries = (fileDeviceKnDiffPool << 16) |
	((fileReadData | fileWriteData) << 14) |
	(uint32(0x804) << 2) |
	methodBuffered
const IOCTLKnDiffPoolReadContent = (fileDeviceKnDiffPool << 16) |
	((fileReadData | fileWriteData) << 14) |
	(uint32(0x805) << 2) |
	methodBuffered
const IOCTLKnDiffPoolSearchContent = (fileDeviceKnDiffPool << 16) |
	((fileReadData | fileWriteData) << 14) |
	(uint32(0x806) << 2) |
	methodBuffered
const IOCTLKnDiffPoolClearSnapshots = (fileDeviceKnDiffPool << 16) |
	((fileReadData | fileWriteData) << 14) |
	(uint32(0x807) << 2) |
	methodBuffered
const IOCTLKnDiffPoolAllocTestPool = (fileDeviceKnDiffPool << 16) |
	((fileReadData | fileWriteData) << 14) |
	(uint32(0x808) << 2) |
	methodBuffered
const IOCTLKnDiffPoolFreeTestPools = (fileDeviceKnDiffPool << 16) |
	((fileReadData | fileWriteData) << 14) |
	(uint32(0x809) << 2) |
	methodBuffered
const IOCTLKnDiffPoolGetCurrentCount = (fileDeviceKnDiffPool << 16) |
	((fileReadData | fileWriteData) << 14) |
	(uint32(0x80A) << 2) |
	methodBuffered
const IOCTLKnDiffPoolGetCurrentEntries = (fileDeviceKnDiffPool << 16) |
	((fileReadData | fileWriteData) << 14) |
	(uint32(0x80B) << 2) |
	methodBuffered
const IOCTLKnDiffPoolGetPTEDetails = (fileDeviceKnDiffPool << 16) |
	((fileReadData | fileWriteData) << 14) |
	(uint32(0x80C) << 2) |
	methodBuffered
const IOCTLKnDiffPoolSetHashPolicy = (fileDeviceKnDiffPool << 16) |
	((fileReadData | fileWriteData) << 14) |
	(uint32(0x80D) << 2) |
	methodBuffered

const (
	FlagNonPaged        = uint32(0x00000001)
	FlagAccessible      = uint32(0x00000002)
	FlagWritable        = uint32(0x00000004)
	FlagExecutable      = uint32(0x00000008)
	FlagAccessibleAll   = uint32(0x00000010)
	FlagWritableAll     = uint32(0x00000020)
	FlagExecutableAny   = uint32(0x00000040)
	FlagWritableMixed   = uint32(0x00000080)
	FlagExecutableMixed = uint32(0x00000100)
	FlagHashChanged     = uint32(0x00000200)
	FlagHashSampled     = uint32(0x00000400)

	SearchTargetDiff    = uint32(0)
	SearchTargetCurrent = uint32(1)

	HashModeSample = uint32(0)
	HashModeFull   = uint32(1)

	PTEFlagPresent    = uint32(0x00000001)
	PTEFlagReadable   = uint32(0x00000002)
	PTEFlagWritable   = uint32(0x00000004)
	PTEFlagExecutable = uint32(0x00000008)
	PTEFlagLargePage  = uint32(0x00000010)

	MaxReadLength     = 4096
	MaxSearchPattern  = 256
	MaxSearchResults  = 1024
	MaxPTEDetailPages = 256
	EntriesHeaderSize = 16
	EntrySize         = 56
	ReadRequestSize   = 24
	ReadHeaderSize    = 24
	SearchRequestSize = 272
	SearchHeaderSize  = 32
	SearchResultSize  = 64
	PTERequestSize    = 16
	PTEHeaderSize     = 32
	PTEPageSize       = 32
	HashPolicySize    = 16

	MaxTestFill           = 128
	DefaultTestSize       = 0x18000
	MaxTestSize           = 0x100000
	TestAllocRequestSize  = 144
	TestAllocResponseSize = 24
)

type PingResponse struct {
	Magic              uint32
	ProtocolVersion    uint32
	DriverVersionMajor uint32
	DriverVersionMinor uint32
}

type CountResponse struct {
	Count    uint32
	Reserved uint32
}

type Entry struct {
	Address         uint64
	Size            uint64
	Tag             uint32
	Flags           uint32
	PageCount       uint32
	AccessiblePages uint32
	WritablePages   uint32
	ExecutablePages uint32
	ContentHash     uint64
	HashedBytes     uint32
	SnapshotID      uint32
}

type EntriesResponseHeader struct {
	Count      uint32
	TotalCount uint32
	Reserved   [2]uint32
}

type ReadRequest struct {
	Address  uint64
	Offset   uint64
	Length   uint32
	Reserved uint32
}

type ReadResponseHeader struct {
	Address   uint64
	Offset    uint64
	BytesRead uint32
	Reserved  uint32
}

type SearchRequest struct {
	PatternLength uint32
	MaxResults    uint32
	Target        uint32
	Reserved      uint32
	Pattern       [MaxSearchPattern]byte
}

type SearchResult struct {
	Address         uint64
	Offset          uint64
	PoolSize        uint64
	Tag             uint32
	Flags           uint32
	PageCount       uint32
	AccessiblePages uint32
	WritablePages   uint32
	ExecutablePages uint32
	ContentHash     uint64
	HashedBytes     uint32
	SnapshotID      uint32
}

type SearchResponseHeader struct {
	Count          uint32
	TotalMatches   uint32
	Truncated      uint32
	EntriesScanned uint32
	EntriesSkipped uint32
	ReadFailures   uint32
	Target         uint32
	Reserved       uint32
}

type TestAllocRequest struct {
	Size       uint32
	Tag        uint32
	FillLength uint32
	Reserved   uint32
	Fill       [MaxTestFill]byte
}

type TestAllocResponse struct {
	Address           uint64
	Size              uint64
	Tag               uint32
	ActiveAllocations uint32
}

type PTERequest struct {
	Address  uint64
	MaxPages uint32
	Reserved uint32
}

type PTEPage struct {
	VirtualAddress uint64
	PageIndex      uint32
	Status         uint32
	Flags          uint32
	Reserved       uint32
	PTEValue       uint64
}

type PTEResponseHeader struct {
	Address       uint64
	PoolSize      uint64
	PageCount     uint32
	ReturnedPages uint32
	Truncated     uint32
	Flags         uint32
}

type HashPolicyRequest struct {
	Mode        uint32
	Reserved    uint32
	SampleBytes uint64
}

func PoolTag(value string) uint32 {
	bytes := []byte(value)
	var tag uint32
	for i := 0; i < 4 && i < len(bytes); i++ {
		tag |= uint32(bytes[i]) << (8 * i)
	}
	return tag
}

func (result SearchResult) TagString() string {
	return tagString(result.Tag)
}

func (entry Entry) TagString() string {
	return tagString(entry.Tag)
}

func SearchTargetName(target uint32) string {
	switch target {
	case SearchTargetDiff:
		return "diff"
	case SearchTargetCurrent:
		return "current"
	default:
		return "unknown"
	}
}

func HashModeName(mode uint32) string {
	switch mode {
	case HashModeSample:
		return "sample"
	case HashModeFull:
		return "full"
	default:
		return "unknown"
	}
}

func tagString(tag uint32) string {
	bytes := []byte{
		byte(tag),
		byte(tag >> 8),
		byte(tag >> 16),
		byte(tag >> 24),
	}

	for i, b := range bytes {
		if b < 0x20 || b > 0x7e {
			bytes[i] = '.'
		}
	}

	return string(bytes)
}
