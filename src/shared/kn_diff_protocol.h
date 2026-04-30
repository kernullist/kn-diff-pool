#pragma once

#define KN_DIFF_POOL_DEVICE_NAME L"\\Device\\KnDiffPool"
#define KN_DIFF_POOL_SYMBOLIC_LINK_NAME L"\\DosDevices\\KnDiffPool"
#define KN_DIFF_POOL_DOS_DEVICE_NAME L"\\\\.\\KnDiffPool"

#define FILE_DEVICE_KN_DIFF_POOL 0x8000

#define KN_DIFF_POOL_PROTOCOL_VERSION 3u
#define KN_DIFF_POOL_MAGIC 0x50444E4Bu

#define IOCTL_KN_DIFF_POOL_PING \
    CTL_CODE(FILE_DEVICE_KN_DIFF_POOL, 0x800, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)
#define IOCTL_KN_DIFF_POOL_CREATE_BASELINE \
    CTL_CODE(FILE_DEVICE_KN_DIFF_POOL, 0x801, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)
#define IOCTL_KN_DIFF_POOL_CREATE_CURRENT \
    CTL_CODE(FILE_DEVICE_KN_DIFF_POOL, 0x802, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)
#define IOCTL_KN_DIFF_POOL_GET_DIFF_COUNT \
    CTL_CODE(FILE_DEVICE_KN_DIFF_POOL, 0x803, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)
#define IOCTL_KN_DIFF_POOL_GET_DIFF_ENTRIES \
    CTL_CODE(FILE_DEVICE_KN_DIFF_POOL, 0x804, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)
#define IOCTL_KN_DIFF_POOL_READ_CONTENT \
    CTL_CODE(FILE_DEVICE_KN_DIFF_POOL, 0x805, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)
#define IOCTL_KN_DIFF_POOL_SEARCH_CONTENT \
    CTL_CODE(FILE_DEVICE_KN_DIFF_POOL, 0x806, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)
#define IOCTL_KN_DIFF_POOL_CLEAR_SNAPSHOTS \
    CTL_CODE(FILE_DEVICE_KN_DIFF_POOL, 0x807, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)
#define IOCTL_KN_DIFF_POOL_ALLOC_TEST_POOL \
    CTL_CODE(FILE_DEVICE_KN_DIFF_POOL, 0x808, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)
#define IOCTL_KN_DIFF_POOL_FREE_TEST_POOLS \
    CTL_CODE(FILE_DEVICE_KN_DIFF_POOL, 0x809, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)
#define IOCTL_KN_DIFF_POOL_GET_CURRENT_COUNT \
    CTL_CODE(FILE_DEVICE_KN_DIFF_POOL, 0x80A, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)
#define IOCTL_KN_DIFF_POOL_GET_CURRENT_ENTRIES \
    CTL_CODE(FILE_DEVICE_KN_DIFF_POOL, 0x80B, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)
#define IOCTL_KN_DIFF_POOL_GET_PTE_DETAILS \
    CTL_CODE(FILE_DEVICE_KN_DIFF_POOL, 0x80C, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)
#define IOCTL_KN_DIFF_POOL_SET_HASH_POLICY \
    CTL_CODE(FILE_DEVICE_KN_DIFF_POOL, 0x80D, METHOD_BUFFERED, FILE_READ_DATA | FILE_WRITE_DATA)

#define KN_DIFF_POOL_FLAG_NONPAGED 0x00000001u
#define KN_DIFF_POOL_FLAG_ACCESSIBLE 0x00000002u
#define KN_DIFF_POOL_FLAG_WRITABLE 0x00000004u
#define KN_DIFF_POOL_FLAG_EXECUTABLE 0x00000008u
#define KN_DIFF_POOL_FLAG_ACCESSIBLE_ALL 0x00000010u
#define KN_DIFF_POOL_FLAG_WRITABLE_ALL 0x00000020u
#define KN_DIFF_POOL_FLAG_EXECUTABLE_ANY 0x00000040u
#define KN_DIFF_POOL_FLAG_WRITABLE_MIXED 0x00000080u
#define KN_DIFF_POOL_FLAG_EXECUTABLE_MIXED 0x00000100u
#define KN_DIFF_POOL_FLAG_HASH_CHANGED 0x00000200u
#define KN_DIFF_POOL_FLAG_HASH_SAMPLED 0x00000400u

#define KN_DIFF_POOL_SEARCH_TARGET_DIFF 0u
#define KN_DIFF_POOL_SEARCH_TARGET_CURRENT 1u

#define KN_DIFF_POOL_HASH_MODE_SAMPLE 0u
#define KN_DIFF_POOL_HASH_MODE_FULL 1u

#define KN_DIFF_POOL_PTE_FLAG_PRESENT 0x00000001u
#define KN_DIFF_POOL_PTE_FLAG_READABLE 0x00000002u
#define KN_DIFF_POOL_PTE_FLAG_WRITABLE 0x00000004u
#define KN_DIFF_POOL_PTE_FLAG_EXECUTABLE 0x00000008u
#define KN_DIFF_POOL_PTE_FLAG_LARGE_PAGE 0x00000010u

#define KN_DIFF_POOL_MAX_READ_LENGTH 4096u
#define KN_DIFF_POOL_MAX_SEARCH_PATTERN 256u
#define KN_DIFF_POOL_MAX_SEARCH_RESULTS 1024u
#define KN_DIFF_POOL_MAX_PTE_DETAIL_PAGES 256u
#define KN_DIFF_POOL_MAX_TEST_FILL 128u
#define KN_DIFF_POOL_DEFAULT_TEST_SIZE 0x18000u
#define KN_DIFF_POOL_MAX_TEST_SIZE 0x100000u
#define KN_DIFF_POOL_MAX_TEST_ALLOCATIONS 32u

typedef struct _KN_DIFF_POOL_PING_RESPONSE {
    unsigned int Magic;
    unsigned int ProtocolVersion;
    unsigned int DriverVersionMajor;
    unsigned int DriverVersionMinor;
} KN_DIFF_POOL_PING_RESPONSE, *PKN_DIFF_POOL_PING_RESPONSE;

typedef struct _KN_DIFF_POOL_COUNT_RESPONSE {
    unsigned int Count;
    unsigned int Reserved;
} KN_DIFF_POOL_COUNT_RESPONSE, *PKN_DIFF_POOL_COUNT_RESPONSE;

typedef struct _KN_DIFF_POOL_ENTRY {
    unsigned __int64 Address;
    unsigned __int64 Size;
    unsigned int Tag;
    unsigned int Flags;
    unsigned int PageCount;
    unsigned int AccessiblePages;
    unsigned int WritablePages;
    unsigned int ExecutablePages;
    unsigned __int64 ContentHash;
    unsigned int HashedBytes;
    unsigned int SnapshotId;
} KN_DIFF_POOL_ENTRY, *PKN_DIFF_POOL_ENTRY;

typedef struct _KN_DIFF_POOL_ENTRIES_RESPONSE {
    unsigned int Count;
    unsigned int TotalCount;
    unsigned int Reserved[2];
    KN_DIFF_POOL_ENTRY Entries[1];
} KN_DIFF_POOL_ENTRIES_RESPONSE, *PKN_DIFF_POOL_ENTRIES_RESPONSE;

typedef struct _KN_DIFF_POOL_READ_REQUEST {
    unsigned __int64 Address;
    unsigned __int64 Offset;
    unsigned int Length;
    unsigned int Reserved;
} KN_DIFF_POOL_READ_REQUEST, *PKN_DIFF_POOL_READ_REQUEST;

typedef struct _KN_DIFF_POOL_READ_RESPONSE {
    unsigned __int64 Address;
    unsigned __int64 Offset;
    unsigned int BytesRead;
    unsigned int Reserved;
    unsigned char Data[1];
} KN_DIFF_POOL_READ_RESPONSE, *PKN_DIFF_POOL_READ_RESPONSE;

typedef struct _KN_DIFF_POOL_SEARCH_REQUEST {
    unsigned int PatternLength;
    unsigned int MaxResults;
    unsigned int Target;
    unsigned int Reserved;
    unsigned char Pattern[KN_DIFF_POOL_MAX_SEARCH_PATTERN];
} KN_DIFF_POOL_SEARCH_REQUEST, *PKN_DIFF_POOL_SEARCH_REQUEST;

typedef struct _KN_DIFF_POOL_SEARCH_RESULT {
    unsigned __int64 Address;
    unsigned __int64 Offset;
    unsigned __int64 PoolSize;
    unsigned int Tag;
    unsigned int Flags;
    unsigned int PageCount;
    unsigned int AccessiblePages;
    unsigned int WritablePages;
    unsigned int ExecutablePages;
    unsigned __int64 ContentHash;
    unsigned int HashedBytes;
    unsigned int SnapshotId;
} KN_DIFF_POOL_SEARCH_RESULT, *PKN_DIFF_POOL_SEARCH_RESULT;

typedef struct _KN_DIFF_POOL_SEARCH_RESPONSE {
    unsigned int Count;
    unsigned int TotalMatches;
    unsigned int Truncated;
    unsigned int EntriesScanned;
    unsigned int EntriesSkipped;
    unsigned int ReadFailures;
    unsigned int Target;
    unsigned int Reserved;
    KN_DIFF_POOL_SEARCH_RESULT Results[1];
} KN_DIFF_POOL_SEARCH_RESPONSE, *PKN_DIFF_POOL_SEARCH_RESPONSE;

typedef struct _KN_DIFF_POOL_TEST_ALLOC_REQUEST {
    unsigned int Size;
    unsigned int Tag;
    unsigned int FillLength;
    unsigned int Reserved;
    unsigned char Fill[KN_DIFF_POOL_MAX_TEST_FILL];
} KN_DIFF_POOL_TEST_ALLOC_REQUEST, *PKN_DIFF_POOL_TEST_ALLOC_REQUEST;

typedef struct _KN_DIFF_POOL_TEST_ALLOC_RESPONSE {
    unsigned __int64 Address;
    unsigned __int64 Size;
    unsigned int Tag;
    unsigned int ActiveAllocations;
} KN_DIFF_POOL_TEST_ALLOC_RESPONSE, *PKN_DIFF_POOL_TEST_ALLOC_RESPONSE;

typedef struct _KN_DIFF_POOL_PTE_REQUEST {
    unsigned __int64 Address;
    unsigned int MaxPages;
    unsigned int Reserved;
} KN_DIFF_POOL_PTE_REQUEST, *PKN_DIFF_POOL_PTE_REQUEST;

typedef struct _KN_DIFF_POOL_PTE_PAGE {
    unsigned __int64 VirtualAddress;
    unsigned int PageIndex;
    unsigned int Status;
    unsigned int Flags;
    unsigned int Reserved;
    unsigned __int64 PteValue;
} KN_DIFF_POOL_PTE_PAGE, *PKN_DIFF_POOL_PTE_PAGE;

typedef struct _KN_DIFF_POOL_PTE_RESPONSE {
    unsigned __int64 Address;
    unsigned __int64 PoolSize;
    unsigned int PageCount;
    unsigned int ReturnedPages;
    unsigned int Truncated;
    unsigned int Flags;
    KN_DIFF_POOL_PTE_PAGE Pages[1];
} KN_DIFF_POOL_PTE_RESPONSE, *PKN_DIFF_POOL_PTE_RESPONSE;

typedef struct _KN_DIFF_POOL_HASH_POLICY_REQUEST {
    unsigned int Mode;
    unsigned int Reserved;
    unsigned __int64 SampleBytes;
} KN_DIFF_POOL_HASH_POLICY_REQUEST, *PKN_DIFF_POOL_HASH_POLICY_REQUEST;
