#include <ntddk.h>
#include <wdmsec.h>
#pragma warning(push)
#pragma warning(disable : 5040)
#include <intrin.h>
#pragma warning(pop)

#include "..\..\shared\kn_diff_protocol.h"

namespace
{
constexpr ULONG KN_DIFF_POOL_TAG = KN_DIFF_POOL_MAGIC;
constexpr ULONG KN_DIFF_POOL_SEARCH_CHUNK_SIZE = 4096;
constexpr ULONG KN_DIFF_POOL_HASH_CHUNK_SIZE = 512;
constexpr ULONG KN_DIFF_POOL_HASH_SAMPLE_MAX = 0x10000;
constexpr ULONG KN_DIFF_POOL_HASH_SAMPLE_MIN = 0x1000;
constexpr ULONG KN_DIFF_POOL_HASH_SAMPLE_LIMIT = 0x1000000;
constexpr ULONG KN_DIFF_BIG_POOL_QUERY_INITIAL_SIZE = 0x10000;
constexpr ULONG KN_DIFF_BIG_POOL_QUERY_MAX_SIZE = 0x4000000;
constexpr ULONG KN_DIFF_BIG_POOL_QUERY_MAX_RETRIES = 16;
constexpr unsigned __int64 KN_DIFF_FNV1A_OFFSET = 14695981039346656037ull;
constexpr unsigned __int64 KN_DIFF_FNV1A_PRIME = 1099511628211ull;
constexpr unsigned __int64 KN_DIFF_PTE_VALID = 1ull << 0;
constexpr unsigned __int64 KN_DIFF_PTE_WRITE = 1ull << 1;
constexpr unsigned __int64 KN_DIFF_PTE_LARGE_PAGE = 1ull << 7;
constexpr unsigned __int64 KN_DIFF_PTE_NX = 1ull << 63;
constexpr unsigned __int64 KN_DIFF_PTE_ADDRESS_MASK = 0x000FFFFFFFFFF000ull;
constexpr unsigned __int64 KN_DIFF_CR4_LA57 = 1ull << 12;
constexpr int KN_DIFF_CPUID_LA57 = 1 << 16;

constexpr ULONG MakePoolTag(UCHAR first, UCHAR second, UCHAR third, UCHAR fourth)
{
    return static_cast<ULONG>(first) |
        (static_cast<ULONG>(second) << 8) |
        (static_cast<ULONG>(third) << 16) |
        (static_cast<ULONG>(fourth) << 24);
}

constexpr ULONG KN_DIFF_TEST_POOL_DEFAULT_TAG = MakePoolTag('K', 'D', 'T', 'P');
constexpr UCHAR KN_DIFF_TEST_POOL_DEFAULT_FILL[] =
    "KN_DIFF_POOL_TEST_PAYLOAD::baseline-to-current-diff::";
constexpr GUID KN_DIFF_POOL_DEVICE_CLASS_GUID =
    { 0x6f5c1b64, 0x5b22, 0x4bde, { 0x93, 0xf1, 0x44, 0x92, 0xa1, 0x2b, 0xde, 0x70 } };

using ZwQuerySystemInformationFn = NTSTATUS(NTAPI*)(
    _In_ ULONG SystemInformationClass,
    _Inout_ PVOID SystemInformation,
    _In_ ULONG SystemInformationLength,
    _Out_opt_ PULONG ReturnLength);

enum SYSTEM_INFORMATION_CLASS_LOCAL : ULONG
{
    SystemBigPoolInformation = 0x42
};

typedef struct _SYSTEM_BIGPOOL_ENTRY_LOCAL
{
    union
    {
        PVOID VirtualAddress;
        ULONG_PTR NonPaged : 1;
    };
    SIZE_T SizeInBytes;
    union
    {
        UCHAR Tag[4];
        ULONG TagUlong;
    };
} SYSTEM_BIGPOOL_ENTRY_LOCAL, *PSYSTEM_BIGPOOL_ENTRY_LOCAL;

typedef struct _SYSTEM_BIGPOOL_INFORMATION_LOCAL
{
    ULONG Count;
    SYSTEM_BIGPOOL_ENTRY_LOCAL Entries[1];
} SYSTEM_BIGPOOL_INFORMATION_LOCAL, *PSYSTEM_BIGPOOL_INFORMATION_LOCAL;

struct Snapshot
{
    PKN_DIFF_POOL_ENTRY Entries;
    ULONG Count;
};

struct PteAttributes
{
    BOOLEAN Present;
    BOOLEAN Writable;
    BOOLEAN Executable;
    BOOLEAN LargePage;
    unsigned __int64 RawEntry;
};

struct TestAllocation
{
    LIST_ENTRY Link;
    PVOID Address;
    SIZE_T Size;
    ULONG Tag;
};

struct SearchStats
{
    ULONG EntriesScanned;
    ULONG EntriesSkipped;
    ULONG ReadFailures;
};

PDEVICE_OBJECT g_DeviceObject = nullptr;
FAST_MUTEX g_SnapshotLock;
FAST_MUTEX g_TestAllocationLock;
FAST_MUTEX g_HashPolicyLock;
LIST_ENTRY g_TestAllocations;
Snapshot g_Baseline = {};
Snapshot g_Current = {};
Snapshot g_Diff = {};
ULONG g_TestAllocationCount = 0;
volatile LONG g_NextSnapshotId = 0;
ULONG g_HashMode = KN_DIFF_POOL_HASH_MODE_SAMPLE;
unsigned __int64 g_HashSampleBytes = KN_DIFF_POOL_HASH_SAMPLE_MAX;
ZwQuerySystemInformationFn g_ZwQuerySystemInformation = nullptr;

void GetHashPolicy(ULONG* mode, unsigned __int64* sampleBytes)
{
    if (mode == nullptr || sampleBytes == nullptr) {
        return;
    }

    ExAcquireFastMutex(&g_HashPolicyLock);
    *mode = g_HashMode;
    *sampleBytes = g_HashSampleBytes;
    ExReleaseFastMutex(&g_HashPolicyLock);
}

NTSTATUS CompleteRequest(PIRP irp, NTSTATUS status, ULONG_PTR information = 0)
{
    irp->IoStatus.Status = status;
    irp->IoStatus.Information = information;
    IoCompleteRequest(irp, IO_NO_INCREMENT);
    return status;
}

void FreeSnapshot(Snapshot* snapshot)
{
    if (snapshot->Entries != nullptr) {
        ExFreePoolWithTag(snapshot->Entries, KN_DIFF_POOL_TAG);
    }

    snapshot->Entries = nullptr;
    snapshot->Count = 0;
}

NTSTATUS AllocateSnapshot(Snapshot* snapshot, ULONG capacity)
{
    snapshot->Entries = nullptr;
    snapshot->Count = 0;

    if (capacity == 0) {
        return STATUS_SUCCESS;
    }

    const SIZE_T allocationSize = sizeof(KN_DIFF_POOL_ENTRY) * static_cast<SIZE_T>(capacity);
    snapshot->Entries = static_cast<PKN_DIFF_POOL_ENTRY>(
        ExAllocatePool2(POOL_FLAG_NON_PAGED, allocationSize, KN_DIFF_POOL_TAG));

    if (snapshot->Entries == nullptr) {
        return STATUS_INSUFFICIENT_RESOURCES;
    }

    RtlZeroMemory(snapshot->Entries, allocationSize);
    return STATUS_SUCCESS;
}

void ReplaceSnapshot(Snapshot* target, Snapshot* source)
{
    FreeSnapshot(target);
    target->Entries = source->Entries;
    target->Count = source->Count;
    source->Entries = nullptr;
    source->Count = 0;
}

ULONG ClearSnapshots()
{
    ULONG clearedEntries = 0;

    ExAcquireFastMutex(&g_SnapshotLock);
    clearedEntries = g_Baseline.Count + g_Current.Count + g_Diff.Count;
    FreeSnapshot(&g_Baseline);
    FreeSnapshot(&g_Current);
    FreeSnapshot(&g_Diff);
    ExReleaseFastMutex(&g_SnapshotLock);

    return clearedEntries;
}

void FillTestAllocation(PVOID address, SIZE_T size, const UCHAR* fill, ULONG fillLength)
{
    if (address == nullptr || size == 0 || fill == nullptr || fillLength == 0) {
        return;
    }

    UCHAR* destination = static_cast<UCHAR*>(address);
    SIZE_T offset = 0;

    while (offset < size) {
        SIZE_T bytesToCopy = fillLength;
        if (bytesToCopy > size - offset) {
            bytesToCopy = size - offset;
        }

        RtlCopyMemory(destination + offset, fill, bytesToCopy);
        offset += bytesToCopy;
    }
}

BOOLEAN AddUnsigned64(unsigned __int64 left, unsigned __int64 right, unsigned __int64* result)
{
    if (result == nullptr) {
        return FALSE;
    }

    *result = left + right;
    return *result >= left;
}

ULONG FreeAllTestAllocations()
{
    LIST_ENTRY localList;
    InitializeListHead(&localList);

    ULONG freed = 0;
    ExAcquireFastMutex(&g_TestAllocationLock);

    while (!IsListEmpty(&g_TestAllocations)) {
        PLIST_ENTRY entry = RemoveHeadList(&g_TestAllocations);
        InsertTailList(&localList, entry);
        ++freed;
    }

    g_TestAllocationCount = 0;
    ExReleaseFastMutex(&g_TestAllocationLock);

    while (!IsListEmpty(&localList)) {
        PLIST_ENTRY entry = RemoveHeadList(&localList);
        TestAllocation* allocation = CONTAINING_RECORD(entry, TestAllocation, Link);

        if (allocation->Address != nullptr) {
            ExFreePoolWithTag(allocation->Address, allocation->Tag);
        }
        ExFreePoolWithTag(allocation, KN_DIFF_POOL_TAG);
    }

    return freed;
}

BOOLEAN EntryIdentityEquals(const KN_DIFF_POOL_ENTRY& lhs, const KN_DIFF_POOL_ENTRY& rhs)
{
    return lhs.Address == rhs.Address &&
        lhs.Size == rhs.Size &&
        lhs.Tag == rhs.Tag;
}

const KN_DIFF_POOL_ENTRY* FindEntryByIdentity(const Snapshot& snapshot, const KN_DIFF_POOL_ENTRY& entry)
{
    for (ULONG i = 0; i < snapshot.Count; ++i) {
        if (EntryIdentityEquals(snapshot.Entries[i], entry)) {
            return &snapshot.Entries[i];
        }
    }

    return nullptr;
}

BOOLEAN EntryContentHashChanged(const KN_DIFF_POOL_ENTRY& baseline, const KN_DIFF_POOL_ENTRY& current)
{
    return baseline.HashedBytes != 0 &&
        current.HashedBytes != 0 &&
        baseline.HashedBytes == current.HashedBytes &&
        ((baseline.Flags ^ current.Flags) & KN_DIFF_POOL_FLAG_HASH_SAMPLED) == 0 &&
        baseline.ContentHash != current.ContentHash;
}

BOOLEAN AddressInPoolEntry(const KN_DIFF_POOL_ENTRY& entry, unsigned __int64 address)
{
    unsigned __int64 endAddress = 0;
    if (!AddUnsigned64(entry.Address, entry.Size, &endAddress)) {
        endAddress = ~static_cast<unsigned __int64>(0);
    }

    return address >= entry.Address && address < endAddress;
}

BOOLEAN FindEntryByAddress(const Snapshot& snapshot, unsigned __int64 address, KN_DIFF_POOL_ENTRY* outEntry)
{
    for (ULONG i = 0; i < snapshot.Count; ++i) {
        if (snapshot.Entries[i].Address == address) {
            *outEntry = snapshot.Entries[i];
            return TRUE;
        }
    }

    return FALSE;
}

BOOLEAN FindEntryContainingAddress(const Snapshot& snapshot, unsigned __int64 address, KN_DIFF_POOL_ENTRY* outEntry)
{
    for (ULONG i = 0; i < snapshot.Count; ++i) {
        if (AddressInPoolEntry(snapshot.Entries[i], address)) {
            *outEntry = snapshot.Entries[i];
            return TRUE;
        }
    }

    return FALSE;
}

BOOLEAN FindReadableEntryByAddressLocked(unsigned __int64 address, KN_DIFF_POOL_ENTRY* outEntry)
{
    if (FindEntryByAddress(g_Diff, address, outEntry)) {
        return TRUE;
    }
    return FindEntryByAddress(g_Current, address, outEntry);
}

BOOLEAN FindPteEntryByAddressLocked(unsigned __int64 address, KN_DIFF_POOL_ENTRY* outEntry)
{
    if (FindEntryContainingAddress(g_Diff, address, outEntry)) {
        return TRUE;
    }
    return FindEntryContainingAddress(g_Current, address, outEntry);
}

NTSTATUS CopyDiffSnapshot(Snapshot* snapshot)
{
    ExAcquireFastMutex(&g_SnapshotLock);

    NTSTATUS status = AllocateSnapshot(snapshot, g_Diff.Count);
    if (NT_SUCCESS(status)) {
        snapshot->Count = g_Diff.Count;
        if (snapshot->Count > 0) {
            RtlCopyMemory(snapshot->Entries, g_Diff.Entries, sizeof(KN_DIFF_POOL_ENTRY) * snapshot->Count);
        }
    }

    ExReleaseFastMutex(&g_SnapshotLock);
    return status;
}

NTSTATUS CopyCurrentSnapshot(Snapshot* snapshot)
{
    ExAcquireFastMutex(&g_SnapshotLock);

    NTSTATUS status = AllocateSnapshot(snapshot, g_Current.Count);
    if (NT_SUCCESS(status)) {
        snapshot->Count = g_Current.Count;
        if (snapshot->Count > 0) {
            RtlCopyMemory(snapshot->Entries, g_Current.Entries, sizeof(KN_DIFF_POOL_ENTRY) * snapshot->Count);
        }
    }

    ExReleaseFastMutex(&g_SnapshotLock);
    return status;
}

NTSTATUS CopySnapshotByTarget(ULONG target, Snapshot* snapshot)
{
    ExAcquireFastMutex(&g_SnapshotLock);

    Snapshot* source = nullptr;
    if (target == KN_DIFF_POOL_SEARCH_TARGET_CURRENT) {
        source = &g_Current;
    }
    else if (target == KN_DIFF_POOL_SEARCH_TARGET_DIFF) {
        source = &g_Diff;
    }

    NTSTATUS status = STATUS_INVALID_PARAMETER;
    if (source != nullptr) {
        status = AllocateSnapshot(snapshot, source->Count);
        if (NT_SUCCESS(status)) {
            snapshot->Count = source->Count;
            if (snapshot->Count > 0) {
                RtlCopyMemory(snapshot->Entries, source->Entries, sizeof(KN_DIFF_POOL_ENTRY) * snapshot->Count);
            }
        }
    }

    ExReleaseFastMutex(&g_SnapshotLock);
    return status;
}

NTSTATUS CopyFromVirtualAddress(
    unsigned __int64 address,
    PVOID destination,
    SIZE_T length,
    PSIZE_T bytesCopied)
{
    *bytesCopied = 0;

    if (length == 0) {
        return STATUS_SUCCESS;
    }
    if (KeGetCurrentIrql() > APC_LEVEL) {
        return STATUS_INVALID_DEVICE_STATE;
    }

    MM_COPY_ADDRESS source = {};
    source.VirtualAddress = reinterpret_cast<PVOID>(static_cast<ULONG_PTR>(address));

    return MmCopyMemory(destination, source, length, MM_COPY_MEMORY_VIRTUAL, bytesCopied);
}

BOOLEAN IsLa57PagingEnabled()
{
    int cpuInfo[4] = {};
    __cpuidex(cpuInfo, 7, 0);

    if ((cpuInfo[2] & KN_DIFF_CPUID_LA57) == 0) {
        return FALSE;
    }

    return (__readcr4() & KN_DIFF_CR4_LA57) != 0;
}

ULONG GetPageTableIndex(unsigned __int64 virtualAddress, ULONG shift)
{
    return static_cast<ULONG>((virtualAddress >> shift) & 0x1ff);
}

NTSTATUS CopyPhysicalPage(unsigned __int64 physicalAddress, unsigned __int64* pageTable)
{
    MM_COPY_ADDRESS source = {};
    source.PhysicalAddress.QuadPart =
        static_cast<LONGLONG>(physicalAddress & KN_DIFF_PTE_ADDRESS_MASK);

    SIZE_T bytesCopied = 0;
    NTSTATUS status = MmCopyMemory(
        pageTable,
        source,
        PAGE_SIZE,
        MM_COPY_MEMORY_PHYSICAL,
        &bytesCopied);

    if (!NT_SUCCESS(status)) {
        return status;
    }
    if (bytesCopied != PAGE_SIZE) {
        return STATUS_PARTIAL_COPY;
    }

    return STATUS_SUCCESS;
}

NTSTATUS QueryAddressPteAttributes(
    unsigned __int64 virtualAddress,
    unsigned __int64* pageTable,
    BOOLEAN la57Enabled,
    PteAttributes* attributes)
{
    if (pageTable == nullptr || attributes == nullptr) {
        return STATUS_INVALID_PARAMETER;
    }
    if (KeGetCurrentIrql() > APC_LEVEL) {
        return STATUS_INVALID_DEVICE_STATE;
    }

    attributes->Present = FALSE;
    attributes->Writable = FALSE;
    attributes->Executable = FALSE;
    attributes->LargePage = FALSE;
    attributes->RawEntry = 0;

    const ULONG shiftsLa48[] = { 39, 30, 21, 12 };
    const ULONG shiftsLa57[] = { 48, 39, 30, 21, 12 };
    const ULONG* shifts = la57Enabled ? shiftsLa57 : shiftsLa48;
    const ULONG shiftCount = la57Enabled ? RTL_NUMBER_OF(shiftsLa57) : RTL_NUMBER_OF(shiftsLa48);

    unsigned __int64 tablePhysicalAddress = __readcr3() & KN_DIFF_PTE_ADDRESS_MASK;
    BOOLEAN writable = TRUE;
    BOOLEAN executable = TRUE;

    NTSTATUS status = CopyPhysicalPage(tablePhysicalAddress, pageTable);
    if (!NT_SUCCESS(status)) {
        return status;
    }

    for (ULONG level = 0; level < shiftCount; ++level) {
        const ULONG shift = shifts[level];
        const ULONG index = GetPageTableIndex(virtualAddress, shift);
        const unsigned __int64 entry = pageTable[index];

        if ((entry & KN_DIFF_PTE_VALID) == 0) {
            return STATUS_INVALID_ADDRESS;
        }

        writable = writable && ((entry & KN_DIFF_PTE_WRITE) != 0);
        executable = executable && ((entry & KN_DIFF_PTE_NX) == 0);

        if ((entry & KN_DIFF_PTE_LARGE_PAGE) != 0 && (shift == 30 || shift == 21)) {
            attributes->Present = TRUE;
            attributes->Writable = writable;
            attributes->Executable = executable;
            attributes->LargePage = TRUE;
            attributes->RawEntry = entry;
            return STATUS_SUCCESS;
        }

        if (shift == 12) {
            attributes->Present = TRUE;
            attributes->Writable = writable;
            attributes->Executable = executable;
            attributes->RawEntry = entry;
            return STATUS_SUCCESS;
        }

        tablePhysicalAddress = entry & KN_DIFF_PTE_ADDRESS_MASK;
        if (tablePhysicalAddress == 0) {
            return STATUS_INVALID_ADDRESS;
        }

        status = CopyPhysicalPage(tablePhysicalAddress, pageTable);
        if (!NT_SUCCESS(status)) {
            return status;
        }
    }

    return STATUS_INVALID_ADDRESS;
}

BOOLEAN IsVirtualAddressReadable(unsigned __int64 address)
{
    UCHAR value = 0;
    SIZE_T bytesCopied = 0;
    NTSTATUS status = CopyFromVirtualAddress(address, &value, sizeof(value), &bytesCopied);
    return NT_SUCCESS(status) && bytesCopied == sizeof(value);
}

ULONG CountPagesForRange(unsigned __int64 address, unsigned __int64 size)
{
    if (size == 0) {
        return 0;
    }

    unsigned __int64 lastAddress = 0;
    if (!AddUnsigned64(address, size - 1, &lastAddress)) {
        lastAddress = ~0ull;
    }

    const unsigned __int64 firstPage = address >> PAGE_SHIFT;
    const unsigned __int64 lastPage = lastAddress >> PAGE_SHIFT;
    const unsigned __int64 pageCount = lastPage - firstPage + 1;
    if (pageCount > MAXULONG) {
        return MAXULONG;
    }

    return static_cast<ULONG>(pageCount);
}

void UpdateContentHash(KN_DIFF_POOL_ENTRY* entry, const UCHAR* buffer, SIZE_T length)
{
    if (entry == nullptr || buffer == nullptr || length == 0) {
        return;
    }

    unsigned __int64 hash = entry->ContentHash;
    if (entry->HashedBytes == 0 && hash == 0) {
        hash = KN_DIFF_FNV1A_OFFSET;
    }

    for (SIZE_T i = 0; i < length; ++i) {
        hash ^= buffer[i];
        hash *= KN_DIFF_FNV1A_PRIME;
    }

    entry->ContentHash = hash;
    const unsigned __int64 newLength = static_cast<unsigned __int64>(entry->HashedBytes) + length;
    entry->HashedBytes = newLength > MAXULONG ? MAXULONG : static_cast<ULONG>(newLength);
}

void MixContentHashValue(KN_DIFF_POOL_ENTRY* entry, unsigned __int64 value)
{
    if (entry == nullptr) {
        return;
    }

    unsigned __int64 hash = entry->ContentHash;
    if (entry->HashedBytes == 0 && hash == 0) {
        hash = KN_DIFF_FNV1A_OFFSET;
    }

    for (ULONG i = 0; i < sizeof(value); ++i) {
        hash ^= static_cast<UCHAR>(value >> (i * 8));
        hash *= KN_DIFF_FNV1A_PRIME;
    }

    entry->ContentHash = hash;
}

void HashPoolEntryRange(KN_DIFF_POOL_ENTRY* entry, UCHAR* buffer, unsigned __int64 startOffset, unsigned __int64 length)
{
    if (entry == nullptr || buffer == nullptr || length == 0 || startOffset >= entry->Size) {
        return;
    }

    unsigned __int64 endOffset = 0;
    if (!AddUnsigned64(startOffset, length, &endOffset) || endOffset > entry->Size) {
        endOffset = entry->Size;
    }

    unsigned __int64 offset = startOffset;

    while (offset < endOffset) {
        ULONG bytesToRead = KN_DIFF_POOL_HASH_CHUNK_SIZE;
        const unsigned __int64 remaining = endOffset - offset;
        if (bytesToRead > remaining) {
            bytesToRead = static_cast<ULONG>(remaining);
        }

        unsigned __int64 sourceAddress = 0;
        if (!AddUnsigned64(entry->Address, offset, &sourceAddress)) {
            break;
        }

        SIZE_T bytesCopied = 0;
        NTSTATUS status = CopyFromVirtualAddress(sourceAddress, buffer, bytesToRead, &bytesCopied);
        if (bytesCopied > 0) {
            MixContentHashValue(entry, offset);
            UpdateContentHash(entry, buffer, bytesCopied);
            offset += bytesCopied;
        }

        if (!NT_SUCCESS(status) || bytesCopied == 0) {
            const unsigned __int64 nextPageAddress =
                ((sourceAddress >> PAGE_SHIFT) + 1) << PAGE_SHIFT;
            if (nextPageAddress <= entry->Address) {
                break;
            }
            const unsigned __int64 nextOffset = nextPageAddress - entry->Address;
            if (nextOffset <= offset) {
                break;
            }
            offset = nextOffset;
        }
    }
}

void HashPoolEntryContent(KN_DIFF_POOL_ENTRY* entry, UCHAR* buffer)
{
    if (entry == nullptr || buffer == nullptr || entry->Size == 0) {
        return;
    }

    MixContentHashValue(entry, entry->Size);
    ULONG hashMode = KN_DIFF_POOL_HASH_MODE_SAMPLE;
    unsigned __int64 sampleBytes = KN_DIFF_POOL_HASH_SAMPLE_MAX;
    GetHashPolicy(&hashMode, &sampleBytes);
    if (sampleBytes < KN_DIFF_POOL_HASH_SAMPLE_MIN) {
        sampleBytes = KN_DIFF_POOL_HASH_SAMPLE_MIN;
    }
    if (sampleBytes > KN_DIFF_POOL_HASH_SAMPLE_LIMIT) {
        sampleBytes = KN_DIFF_POOL_HASH_SAMPLE_LIMIT;
    }

    if (hashMode == KN_DIFF_POOL_HASH_MODE_FULL || entry->Size <= sampleBytes) {
        HashPoolEntryRange(entry, buffer, 0, entry->Size);
        if (entry->HashedBytes < entry->Size) {
            entry->Flags |= KN_DIFF_POOL_FLAG_HASH_SAMPLED;
        }
        return;
    }

    entry->Flags |= KN_DIFF_POOL_FLAG_HASH_SAMPLED;
    const unsigned __int64 halfSample = sampleBytes / 2;
    HashPoolEntryRange(entry, buffer, 0, halfSample);
    HashPoolEntryRange(entry, buffer, entry->Size - halfSample, halfSample);
}

void AnnotatePoolEntryAttributes(KN_DIFF_POOL_ENTRY* entry, unsigned __int64* pageTable, BOOLEAN la57Enabled)
{
    if (entry == nullptr) {
        return;
    }

    entry->PageCount = CountPagesForRange(entry->Address, entry->Size);
    if (pageTable == nullptr) {
        if (entry->Size > 0 && IsVirtualAddressReadable(entry->Address)) {
            entry->Flags |= KN_DIFF_POOL_FLAG_ACCESSIBLE;
            entry->AccessiblePages = entry->PageCount > 0 ? 1 : 0;
        }
        return;
    }

    for (ULONG page = 0; page < entry->PageCount; ++page) {
        unsigned __int64 pageAddress = 0;
        if (!AddUnsigned64(entry->Address & ~(static_cast<unsigned __int64>(PAGE_SIZE) - 1), static_cast<unsigned __int64>(page) << PAGE_SHIFT, &pageAddress)) {
            break;
        }

        PteAttributes attributes = {};
        NTSTATUS status = QueryAddressPteAttributes(pageAddress, pageTable, la57Enabled, &attributes);
        if (!NT_SUCCESS(status) || !attributes.Present) {
            continue;
        }

        if (IsVirtualAddressReadable(page == 0 ? entry->Address : pageAddress)) {
            ++entry->AccessiblePages;
        }
        if (attributes.Writable) {
            ++entry->WritablePages;
        }
        if (attributes.Executable) {
            ++entry->ExecutablePages;
        }

        if (page == 0) {
            if (entry->AccessiblePages > 0) {
                entry->Flags |= KN_DIFF_POOL_FLAG_ACCESSIBLE;
            }
            if (attributes.Writable) {
                entry->Flags |= KN_DIFF_POOL_FLAG_WRITABLE;
            }
            if (attributes.Executable) {
                entry->Flags |= KN_DIFF_POOL_FLAG_EXECUTABLE;
            }
        }
    }

    if (entry->PageCount != 0 && entry->AccessiblePages == entry->PageCount) {
        entry->Flags |= KN_DIFF_POOL_FLAG_ACCESSIBLE_ALL;
    }
    if (entry->PageCount != 0 && entry->WritablePages == entry->PageCount) {
        entry->Flags |= KN_DIFF_POOL_FLAG_WRITABLE_ALL;
    }
    if (entry->ExecutablePages > 0) {
        entry->Flags |= KN_DIFF_POOL_FLAG_EXECUTABLE_ANY;
    }
    if (entry->WritablePages > 0 && entry->WritablePages < entry->PageCount) {
        entry->Flags |= KN_DIFF_POOL_FLAG_WRITABLE_MIXED;
    }
    if (entry->ExecutablePages > 0 && entry->ExecutablePages < entry->PageCount) {
        entry->Flags |= KN_DIFF_POOL_FLAG_EXECUTABLE_MIXED;
    }
}

NTSTATUS EnsureZwQuerySystemInformation()
{
    if (g_ZwQuerySystemInformation != nullptr) {
        return STATUS_SUCCESS;
    }

    UNICODE_STRING routineName;
    RtlInitUnicodeString(&routineName, L"ZwQuerySystemInformation");

    g_ZwQuerySystemInformation =
        reinterpret_cast<ZwQuerySystemInformationFn>(MmGetSystemRoutineAddress(&routineName));

    return g_ZwQuerySystemInformation != nullptr ? STATUS_SUCCESS : STATUS_PROCEDURE_NOT_FOUND;
}

NTSTATUS QuerySystemBigPoolInfo(PSYSTEM_BIGPOOL_INFORMATION_LOCAL* outBuffer)
{
    *outBuffer = nullptr;

    NTSTATUS status = EnsureZwQuerySystemInformation();
    if (!NT_SUCCESS(status)) {
        return status;
    }

    ULONG bufferSize = KN_DIFF_BIG_POOL_QUERY_INITIAL_SIZE;
    ULONG returnLength = 0;
    PVOID buffer = nullptr;
    ULONG retries = 0;

    for (;;) {
        if (buffer != nullptr) {
            ExFreePoolWithTag(buffer, KN_DIFF_POOL_TAG);
            buffer = nullptr;
        }

        buffer = ExAllocatePool2(POOL_FLAG_NON_PAGED, bufferSize, KN_DIFF_POOL_TAG);
        if (buffer == nullptr) {
            return STATUS_INSUFFICIENT_RESOURCES;
        }

        status = g_ZwQuerySystemInformation(
            SystemBigPoolInformation,
            buffer,
            bufferSize,
            &returnLength);

        if (status == STATUS_INFO_LENGTH_MISMATCH || status == STATUS_BUFFER_TOO_SMALL) {
            if (++retries > KN_DIFF_BIG_POOL_QUERY_MAX_RETRIES) {
                ExFreePoolWithTag(buffer, KN_DIFF_POOL_TAG);
                return STATUS_RETRY;
            }

            ULONG nextSize = bufferSize;
            if (returnLength > bufferSize) {
                nextSize = returnLength;
            }
            else {
                if (bufferSize > KN_DIFF_BIG_POOL_QUERY_MAX_SIZE / 2) {
                    ExFreePoolWithTag(buffer, KN_DIFF_POOL_TAG);
                    return STATUS_INSUFFICIENT_RESOURCES;
                }
                nextSize = bufferSize * 2;
            }

            if (nextSize > KN_DIFF_BIG_POOL_QUERY_MAX_SIZE) {
                ExFreePoolWithTag(buffer, KN_DIFF_POOL_TAG);
                return STATUS_INSUFFICIENT_RESOURCES;
            }

            bufferSize = nextSize;
            continue;
        }

        break;
    }

    if (!NT_SUCCESS(status)) {
        ExFreePoolWithTag(buffer, KN_DIFF_POOL_TAG);
        return status;
    }

    *outBuffer = static_cast<PSYSTEM_BIGPOOL_INFORMATION_LOCAL>(buffer);
    return STATUS_SUCCESS;
}

NTSTATUS ScanBigPoolSnapshot(Snapshot* snapshot, ULONG snapshotId)
{
    PSYSTEM_BIGPOOL_INFORMATION_LOCAL bigPoolInfo = nullptr;
    NTSTATUS status = QuerySystemBigPoolInfo(&bigPoolInfo);
    if (!NT_SUCCESS(status)) {
        return status;
    }

    status = AllocateSnapshot(snapshot, bigPoolInfo->Count);
    if (!NT_SUCCESS(status)) {
        ExFreePoolWithTag(bigPoolInfo, KN_DIFF_POOL_TAG);
        return status;
    }

    unsigned __int64* pageTable = static_cast<unsigned __int64*>(
        ExAllocatePool2(POOL_FLAG_NON_PAGED, PAGE_SIZE, KN_DIFF_POOL_TAG));
    const BOOLEAN la57Enabled = pageTable != nullptr ? IsLa57PagingEnabled() : FALSE;
    UCHAR* hashBuffer = static_cast<UCHAR*>(
        ExAllocatePool2(POOL_FLAG_NON_PAGED, KN_DIFF_POOL_HASH_CHUNK_SIZE, KN_DIFF_POOL_TAG));

    for (ULONG i = 0; i < bigPoolInfo->Count; ++i) {
        const SYSTEM_BIGPOOL_ENTRY_LOCAL* source = &bigPoolInfo->Entries[i];
        if (!source->NonPaged) {
            continue;
        }

        KN_DIFF_POOL_ENTRY* entry = &snapshot->Entries[snapshot->Count++];
        entry->Address = static_cast<unsigned __int64>(
            reinterpret_cast<ULONG_PTR>(source->VirtualAddress) & ~static_cast<ULONG_PTR>(1));
        entry->Size = static_cast<unsigned __int64>(source->SizeInBytes);
        entry->Tag = source->TagUlong;
        entry->Flags = KN_DIFF_POOL_FLAG_NONPAGED;
        entry->SnapshotId = snapshotId;
        AnnotatePoolEntryAttributes(entry, pageTable, la57Enabled);
        HashPoolEntryContent(entry, hashBuffer);
    }

    if (hashBuffer != nullptr) {
        ExFreePoolWithTag(hashBuffer, KN_DIFF_POOL_TAG);
    }
    if (pageTable != nullptr) {
        ExFreePoolWithTag(pageTable, KN_DIFF_POOL_TAG);
    }

    ExFreePoolWithTag(bigPoolInfo, KN_DIFF_POOL_TAG);
    return STATUS_SUCCESS;
}

NTSTATUS RebuildDiffLocked()
{
    FreeSnapshot(&g_Diff);

    NTSTATUS status = AllocateSnapshot(&g_Diff, g_Current.Count);
    if (!NT_SUCCESS(status)) {
        return status;
    }

    for (ULONG i = 0; i < g_Current.Count; ++i) {
        const KN_DIFF_POOL_ENTRY& entry = g_Current.Entries[i];
        const KN_DIFF_POOL_ENTRY* baselineEntry = FindEntryByIdentity(g_Baseline, entry);
        if (baselineEntry == nullptr) {
            g_Diff.Entries[g_Diff.Count++] = entry;
        }
        else if (EntryContentHashChanged(*baselineEntry, entry)) {
            g_Diff.Entries[g_Diff.Count] = entry;
            g_Diff.Entries[g_Diff.Count].Flags |= KN_DIFF_POOL_FLAG_HASH_CHANGED;
            ++g_Diff.Count;
        }
    }

    return STATUS_SUCCESS;
}

NTSTATUS CompleteCountResponse(PIRP irp, ULONG outputBufferLength, ULONG count)
{
    if (outputBufferLength == 0) {
        return CompleteRequest(irp, STATUS_SUCCESS);
    }
    if (outputBufferLength < sizeof(KN_DIFF_POOL_COUNT_RESPONSE)) {
        return CompleteRequest(irp, STATUS_BUFFER_TOO_SMALL);
    }

    auto response = static_cast<PKN_DIFF_POOL_COUNT_RESPONSE>(irp->AssociatedIrp.SystemBuffer);
    if (response == nullptr) {
        return CompleteRequest(irp, STATUS_INVALID_USER_BUFFER);
    }

    response->Count = count;
    response->Reserved = 0;

    return CompleteRequest(irp, STATUS_SUCCESS, sizeof(*response));
}

NTSTATUS DispatchUnsupported(PDEVICE_OBJECT deviceObject, PIRP irp)
{
    UNREFERENCED_PARAMETER(deviceObject);
    return CompleteRequest(irp, STATUS_INVALID_DEVICE_REQUEST);
}

NTSTATUS DispatchCreateClose(PDEVICE_OBJECT deviceObject, PIRP irp)
{
    UNREFERENCED_PARAMETER(deviceObject);
    return CompleteRequest(irp, STATUS_SUCCESS);
}

NTSTATUS HandlePing(PIRP irp, ULONG outputBufferLength)
{
    if (outputBufferLength < sizeof(KN_DIFF_POOL_PING_RESPONSE)) {
        return CompleteRequest(irp, STATUS_BUFFER_TOO_SMALL);
    }

    auto response = static_cast<PKN_DIFF_POOL_PING_RESPONSE>(irp->AssociatedIrp.SystemBuffer);
    if (response == nullptr) {
        return CompleteRequest(irp, STATUS_INVALID_USER_BUFFER);
    }

    RtlZeroMemory(response, sizeof(*response));
    response->Magic = KN_DIFF_POOL_MAGIC;
    response->ProtocolVersion = KN_DIFF_POOL_PROTOCOL_VERSION;
    response->DriverVersionMajor = 0;
    response->DriverVersionMinor = 1;

    return CompleteRequest(irp, STATUS_SUCCESS, sizeof(*response));
}

NTSTATUS HandleCreateBaseline(PIRP irp, ULONG outputBufferLength)
{
    Snapshot snapshot = {};
    const ULONG snapshotId = static_cast<ULONG>(InterlockedIncrement(&g_NextSnapshotId));
    NTSTATUS status = ScanBigPoolSnapshot(&snapshot, snapshotId);
    if (!NT_SUCCESS(status)) {
        return CompleteRequest(irp, status);
    }

    ULONG count = 0;
    ExAcquireFastMutex(&g_SnapshotLock);
    ReplaceSnapshot(&g_Baseline, &snapshot);
    FreeSnapshot(&g_Current);
    FreeSnapshot(&g_Diff);
    count = g_Baseline.Count;
    ExReleaseFastMutex(&g_SnapshotLock);

    FreeSnapshot(&snapshot);
    return CompleteCountResponse(irp, outputBufferLength, count);
}

NTSTATUS HandleCreateCurrent(PIRP irp, ULONG outputBufferLength)
{
    Snapshot snapshot = {};
    const ULONG snapshotId = static_cast<ULONG>(InterlockedIncrement(&g_NextSnapshotId));
    NTSTATUS status = ScanBigPoolSnapshot(&snapshot, snapshotId);
    if (!NT_SUCCESS(status)) {
        return CompleteRequest(irp, status);
    }

    ULONG count = 0;
    ExAcquireFastMutex(&g_SnapshotLock);
    ReplaceSnapshot(&g_Current, &snapshot);
    status = RebuildDiffLocked();
    count = g_Diff.Count;
    ExReleaseFastMutex(&g_SnapshotLock);

    FreeSnapshot(&snapshot);
    if (!NT_SUCCESS(status)) {
        return CompleteRequest(irp, status);
    }

    return CompleteCountResponse(irp, outputBufferLength, count);
}

NTSTATUS HandleGetDiffCount(PIRP irp, ULONG outputBufferLength)
{
    ULONG count = 0;

    ExAcquireFastMutex(&g_SnapshotLock);
    count = g_Diff.Count;
    ExReleaseFastMutex(&g_SnapshotLock);

    return CompleteCountResponse(irp, outputBufferLength, count);
}

NTSTATUS HandleGetCurrentCount(PIRP irp, ULONG outputBufferLength)
{
    ULONG count = 0;

    ExAcquireFastMutex(&g_SnapshotLock);
    count = g_Current.Count;
    ExReleaseFastMutex(&g_SnapshotLock);

    return CompleteCountResponse(irp, outputBufferLength, count);
}

NTSTATUS CompleteEntriesResponse(PIRP irp, ULONG outputBufferLength, const Snapshot& snapshot)
{
    const ULONG headerSize = FIELD_OFFSET(KN_DIFF_POOL_ENTRIES_RESPONSE, Entries);
    if (outputBufferLength < headerSize) {
        return CompleteRequest(irp, STATUS_BUFFER_TOO_SMALL);
    }

    auto response = static_cast<PKN_DIFF_POOL_ENTRIES_RESPONSE>(irp->AssociatedIrp.SystemBuffer);
    if (response == nullptr) {
        return CompleteRequest(irp, STATUS_INVALID_USER_BUFFER);
    }

    RtlZeroMemory(response, headerSize);

    const ULONG capacity = (outputBufferLength - headerSize) / sizeof(KN_DIFF_POOL_ENTRY);
    ULONG copied = 0;
    ULONG total = 0;

    total = snapshot.Count;
    copied = capacity < snapshot.Count ? capacity : snapshot.Count;

    if (copied > 0) {
        RtlCopyMemory(response->Entries, snapshot.Entries, sizeof(KN_DIFF_POOL_ENTRY) * copied);
    }

    response->Count = copied;
    response->TotalCount = total;

    return CompleteRequest(
        irp,
        STATUS_SUCCESS,
        headerSize + sizeof(KN_DIFF_POOL_ENTRY) * copied);
}

NTSTATUS HandleGetDiffEntries(PIRP irp, ULONG outputBufferLength)
{
    Snapshot snapshot = {};
    NTSTATUS status = CopyDiffSnapshot(&snapshot);
    if (!NT_SUCCESS(status)) {
        return CompleteRequest(irp, status);
    }

    status = CompleteEntriesResponse(irp, outputBufferLength, snapshot);
    FreeSnapshot(&snapshot);
    return status;
}

NTSTATUS HandleGetCurrentEntries(PIRP irp, ULONG outputBufferLength)
{
    Snapshot snapshot = {};
    NTSTATUS status = CopyCurrentSnapshot(&snapshot);
    if (!NT_SUCCESS(status)) {
        return CompleteRequest(irp, status);
    }

    status = CompleteEntriesResponse(irp, outputBufferLength, snapshot);
    FreeSnapshot(&snapshot);
    return status;
}

NTSTATUS HandleReadContent(PIRP irp, ULONG inputBufferLength, ULONG outputBufferLength)
{
    const ULONG headerSize = FIELD_OFFSET(KN_DIFF_POOL_READ_RESPONSE, Data);
    if (inputBufferLength < sizeof(KN_DIFF_POOL_READ_REQUEST)) {
        return CompleteRequest(irp, STATUS_INVALID_PARAMETER);
    }
    if (outputBufferLength < headerSize) {
        return CompleteRequest(irp, STATUS_BUFFER_TOO_SMALL);
    }

    auto systemBuffer = irp->AssociatedIrp.SystemBuffer;
    if (systemBuffer == nullptr) {
        return CompleteRequest(irp, STATUS_INVALID_USER_BUFFER);
    }

    KN_DIFF_POOL_READ_REQUEST request = *static_cast<PKN_DIFF_POOL_READ_REQUEST>(systemBuffer);
    KN_DIFF_POOL_ENTRY entry = {};

    ExAcquireFastMutex(&g_SnapshotLock);
    const BOOLEAN found = FindReadableEntryByAddressLocked(request.Address, &entry);
    ExReleaseFastMutex(&g_SnapshotLock);

    if (!found) {
        return CompleteRequest(irp, STATUS_NOT_FOUND);
    }
    if (request.Offset > entry.Size) {
        return CompleteRequest(irp, STATUS_INVALID_PARAMETER);
    }

    ULONG bytesToRead = request.Length;
    if (bytesToRead > KN_DIFF_POOL_MAX_READ_LENGTH) {
        bytesToRead = KN_DIFF_POOL_MAX_READ_LENGTH;
    }

    const ULONG outputCapacity = outputBufferLength - headerSize;
    if (bytesToRead > outputCapacity) {
        bytesToRead = outputCapacity;
    }

    const unsigned __int64 available = entry.Size - request.Offset;
    if (bytesToRead > available) {
        bytesToRead = static_cast<ULONG>(available);
    }

    auto response = static_cast<PKN_DIFF_POOL_READ_RESPONSE>(systemBuffer);
    RtlZeroMemory(response, headerSize);
    response->Address = request.Address;
    response->Offset = request.Offset;

    if (bytesToRead == 0) {
        return CompleteRequest(irp, STATUS_SUCCESS, headerSize);
    }

    unsigned __int64 sourceAddress = 0;
    if (!AddUnsigned64(request.Address, request.Offset, &sourceAddress)) {
        return CompleteRequest(irp, STATUS_INTEGER_OVERFLOW);
    }

    SIZE_T bytesCopied = 0;
    NTSTATUS status = CopyFromVirtualAddress(sourceAddress, response->Data, bytesToRead, &bytesCopied);
    response->BytesRead = static_cast<unsigned int>(bytesCopied);

    if (!NT_SUCCESS(status) && bytesCopied == 0) {
        return CompleteRequest(irp, status, headerSize);
    }

    return CompleteRequest(irp, STATUS_SUCCESS, headerSize + bytesCopied);
}

void FillPtePageInfo(
    KN_DIFF_POOL_PTE_PAGE* pageInfo,
    ULONG pageIndex,
    unsigned __int64 pageAddress,
    const PteAttributes& attributes,
    NTSTATUS status,
    BOOLEAN readable)
{
    pageInfo->VirtualAddress = pageAddress;
    pageInfo->PageIndex = pageIndex;
    pageInfo->Status = static_cast<ULONG>(status);
    pageInfo->PteValue = attributes.RawEntry;

    if (NT_SUCCESS(status) && attributes.Present) {
        pageInfo->Flags |= KN_DIFF_POOL_PTE_FLAG_PRESENT;
    }
    if (readable) {
        pageInfo->Flags |= KN_DIFF_POOL_PTE_FLAG_READABLE;
    }
    if (attributes.Writable) {
        pageInfo->Flags |= KN_DIFF_POOL_PTE_FLAG_WRITABLE;
    }
    if (attributes.Executable) {
        pageInfo->Flags |= KN_DIFF_POOL_PTE_FLAG_EXECUTABLE;
    }
    if (attributes.LargePage) {
        pageInfo->Flags |= KN_DIFF_POOL_PTE_FLAG_LARGE_PAGE;
    }
}

NTSTATUS HandleGetPteDetails(PIRP irp, ULONG inputBufferLength, ULONG outputBufferLength)
{
    const ULONG headerSize = FIELD_OFFSET(KN_DIFF_POOL_PTE_RESPONSE, Pages);
    if (inputBufferLength < sizeof(KN_DIFF_POOL_PTE_REQUEST)) {
        return CompleteRequest(irp, STATUS_INVALID_PARAMETER);
    }
    if (outputBufferLength < headerSize) {
        return CompleteRequest(irp, STATUS_BUFFER_TOO_SMALL);
    }

    auto systemBuffer = irp->AssociatedIrp.SystemBuffer;
    if (systemBuffer == nullptr) {
        return CompleteRequest(irp, STATUS_INVALID_USER_BUFFER);
    }

    KN_DIFF_POOL_PTE_REQUEST request = *static_cast<PKN_DIFF_POOL_PTE_REQUEST>(systemBuffer);
    KN_DIFF_POOL_ENTRY entry = {};

    ExAcquireFastMutex(&g_SnapshotLock);
    const BOOLEAN found = FindPteEntryByAddressLocked(request.Address, &entry);
    ExReleaseFastMutex(&g_SnapshotLock);

    if (!found) {
        return CompleteRequest(irp, STATUS_NOT_FOUND);
    }

    const ULONG outputCapacity = (outputBufferLength - headerSize) / sizeof(KN_DIFF_POOL_PTE_PAGE);
    ULONG requestedPages = request.MaxPages;
    if (requestedPages == 0 || requestedPages > KN_DIFF_POOL_MAX_PTE_DETAIL_PAGES) {
        requestedPages = KN_DIFF_POOL_MAX_PTE_DETAIL_PAGES;
    }

    ULONG pagesToReturn = entry.PageCount;
    if (pagesToReturn > outputCapacity) {
        pagesToReturn = outputCapacity;
    }
    if (pagesToReturn > requestedPages) {
        pagesToReturn = requestedPages;
    }

    auto response = static_cast<PKN_DIFF_POOL_PTE_RESPONSE>(systemBuffer);
    RtlZeroMemory(response, headerSize + sizeof(KN_DIFF_POOL_PTE_PAGE) * pagesToReturn);
    response->Address = entry.Address;
    response->PoolSize = entry.Size;
    response->PageCount = entry.PageCount;
    response->Flags = entry.Flags;

    if (pagesToReturn == 0) {
        response->Truncated = entry.PageCount != 0 ? 1 : 0;
        return CompleteRequest(irp, STATUS_SUCCESS, headerSize);
    }

    unsigned __int64* pageTable = static_cast<unsigned __int64*>(
        ExAllocatePool2(POOL_FLAG_NON_PAGED, PAGE_SIZE, KN_DIFF_POOL_TAG));
    if (pageTable == nullptr) {
        return CompleteRequest(irp, STATUS_INSUFFICIENT_RESOURCES);
    }

    const BOOLEAN la57Enabled = IsLa57PagingEnabled();
    const unsigned __int64 basePageAddress =
        entry.Address & ~(static_cast<unsigned __int64>(PAGE_SIZE) - 1);
    ULONG returnedPages = 0;

    for (ULONG page = 0; page < pagesToReturn; ++page) {
        unsigned __int64 pageAddress = 0;
        if (!AddUnsigned64(basePageAddress, static_cast<unsigned __int64>(page) << PAGE_SHIFT, &pageAddress)) {
            break;
        }

        PteAttributes attributes = {};
        NTSTATUS status = QueryAddressPteAttributes(pageAddress, pageTable, la57Enabled, &attributes);
        const BOOLEAN readable = IsVirtualAddressReadable(page == 0 ? entry.Address : pageAddress);
        FillPtePageInfo(&response->Pages[page], page, pageAddress, attributes, status, readable);
        ++returnedPages;
    }

    response->ReturnedPages = returnedPages;
    response->Truncated = returnedPages < entry.PageCount ? 1 : 0;

    ExFreePoolWithTag(pageTable, KN_DIFF_POOL_TAG);
    return CompleteRequest(
        irp,
        STATUS_SUCCESS,
        headerSize + sizeof(KN_DIFF_POOL_PTE_PAGE) * returnedPages);
}

BOOLEAN PatternEquals(const UCHAR* left, const UCHAR* right, ULONG length)
{
    return RtlCompareMemory(left, right, length) == length;
}

NTSTATUS AppendSearchResult(
    PKN_DIFF_POOL_SEARCH_RESPONSE response,
    ULONG capacity,
    ULONG* storedCount,
    const KN_DIFF_POOL_ENTRY& entry,
    unsigned __int64 offset)
{
    if (*storedCount >= capacity) {
        return STATUS_BUFFER_OVERFLOW;
    }

    KN_DIFF_POOL_SEARCH_RESULT* result = &response->Results[*storedCount];
    result->Address = entry.Address;
    result->Offset = offset;
    result->PoolSize = entry.Size;
    result->Tag = entry.Tag;
    result->Flags = entry.Flags;
    result->PageCount = entry.PageCount;
    result->AccessiblePages = entry.AccessiblePages;
    result->WritablePages = entry.WritablePages;
    result->ExecutablePages = entry.ExecutablePages;
    result->ContentHash = entry.ContentHash;
    result->HashedBytes = entry.HashedBytes;
    result->SnapshotId = entry.SnapshotId;
    ++(*storedCount);

    return STATUS_SUCCESS;
}

NTSTATUS SearchEntry(
    const KN_DIFF_POOL_ENTRY& entry,
    const UCHAR* pattern,
    ULONG patternLength,
    PKN_DIFF_POOL_SEARCH_RESPONSE response,
    ULONG capacity,
    ULONG* storedCount,
    ULONG* totalMatches,
    SearchStats* stats,
    BOOLEAN* truncated)
{
    const ULONG bufferLength = KN_DIFF_POOL_SEARCH_CHUNK_SIZE + patternLength - 1;
    UCHAR* buffer = static_cast<UCHAR*>(ExAllocatePool2(POOL_FLAG_NON_PAGED, bufferLength, KN_DIFF_POOL_TAG));
    if (buffer == nullptr) {
        return STATUS_INSUFFICIENT_RESOURCES;
    }

    ULONG prefixLength = 0;
    unsigned __int64 offset = 0;
    BOOLEAN readAny = FALSE;
    BOOLEAN readFailure = FALSE;

    while (offset < entry.Size) {
        ULONG bytesToRead = KN_DIFF_POOL_SEARCH_CHUNK_SIZE;
        const unsigned __int64 remaining = entry.Size - offset;
        if (bytesToRead > remaining) {
            bytesToRead = static_cast<ULONG>(remaining);
        }

        SIZE_T bytesCopied = 0;
        unsigned __int64 chunkAddress = 0;
        if (!AddUnsigned64(entry.Address, offset, &chunkAddress)) {
            ExFreePoolWithTag(buffer, KN_DIFF_POOL_TAG);
            return STATUS_INTEGER_OVERFLOW;
        }

        NTSTATUS status = CopyFromVirtualAddress(
            chunkAddress,
            buffer + prefixLength,
            bytesToRead,
            &bytesCopied);

        if (!NT_SUCCESS(status)) {
            readFailure = TRUE;
        }
        if (!NT_SUCCESS(status) && bytesCopied == 0) {
            const unsigned __int64 nextPageAddress =
                ((chunkAddress >> PAGE_SHIFT) + 1) << PAGE_SHIFT;
            if (nextPageAddress <= entry.Address) {
                break;
            }
            offset = nextPageAddress - entry.Address;
            prefixLength = 0;
            continue;
        }
        if (bytesCopied > 0) {
            readAny = TRUE;
        }

        const ULONG scanLength = prefixLength + static_cast<ULONG>(bytesCopied);
        if (scanLength >= patternLength) {
            for (ULONG scanIndex = 0; scanIndex + patternLength <= scanLength; ++scanIndex) {
                if (prefixLength != 0 && scanIndex + patternLength <= prefixLength) {
                    continue;
                }

                if (!PatternEquals(buffer + scanIndex, pattern, patternLength)) {
                    continue;
                }

                const unsigned __int64 matchOffset = offset - prefixLength + scanIndex;
                ++(*totalMatches);

                NTSTATUS appendStatus = AppendSearchResult(
                    response,
                    capacity,
                    storedCount,
                    entry,
                    matchOffset);

                if (!NT_SUCCESS(appendStatus)) {
                    *truncated = TRUE;
                    ExFreePoolWithTag(buffer, KN_DIFF_POOL_TAG);
                    return STATUS_SUCCESS;
                }
            }
        }

        if (bytesCopied == 0) {
            break;
        }

        offset += bytesCopied;

        prefixLength = patternLength > 1 ? patternLength - 1 : 0;
        if (prefixLength > scanLength) {
            prefixLength = scanLength;
        }
        if (prefixLength > 0) {
            RtlMoveMemory(buffer, buffer + scanLength - prefixLength, prefixLength);
        }
    }

    if (stats != nullptr) {
        if (!readAny) {
            ++stats->EntriesSkipped;
        }
        if (readFailure) {
            ++stats->ReadFailures;
        }
    }

    ExFreePoolWithTag(buffer, KN_DIFF_POOL_TAG);
    return STATUS_SUCCESS;
}

NTSTATUS HandleSearchContent(PIRP irp, ULONG inputBufferLength, ULONG outputBufferLength)
{
    const ULONG headerSize = FIELD_OFFSET(KN_DIFF_POOL_SEARCH_RESPONSE, Results);
    if (inputBufferLength < sizeof(KN_DIFF_POOL_SEARCH_REQUEST)) {
        return CompleteRequest(irp, STATUS_INVALID_PARAMETER);
    }
    if (outputBufferLength < headerSize) {
        return CompleteRequest(irp, STATUS_BUFFER_TOO_SMALL);
    }

    auto systemBuffer = irp->AssociatedIrp.SystemBuffer;
    if (systemBuffer == nullptr) {
        return CompleteRequest(irp, STATUS_INVALID_USER_BUFFER);
    }

    KN_DIFF_POOL_SEARCH_REQUEST request = *static_cast<PKN_DIFF_POOL_SEARCH_REQUEST>(systemBuffer);
    if (request.PatternLength == 0 || request.PatternLength > KN_DIFF_POOL_MAX_SEARCH_PATTERN) {
        return CompleteRequest(irp, STATUS_INVALID_PARAMETER);
    }
    if (request.Target != KN_DIFF_POOL_SEARCH_TARGET_DIFF &&
        request.Target != KN_DIFF_POOL_SEARCH_TARGET_CURRENT) {
        return CompleteRequest(irp, STATUS_INVALID_PARAMETER);
    }

    const ULONG outputCapacity = (outputBufferLength - headerSize) / sizeof(KN_DIFF_POOL_SEARCH_RESULT);
    ULONG requestedCapacity = request.MaxResults;
    if (requestedCapacity == 0 || requestedCapacity > KN_DIFF_POOL_MAX_SEARCH_RESULTS) {
        requestedCapacity = KN_DIFF_POOL_MAX_SEARCH_RESULTS;
    }
    ULONG capacity = outputCapacity < requestedCapacity ? outputCapacity : requestedCapacity;

    auto response = static_cast<PKN_DIFF_POOL_SEARCH_RESPONSE>(systemBuffer);
    RtlZeroMemory(response, headerSize);

    Snapshot targetSnapshot = {};
    NTSTATUS status = CopySnapshotByTarget(request.Target, &targetSnapshot);
    if (!NT_SUCCESS(status)) {
        return CompleteRequest(irp, status);
    }

    ULONG storedCount = 0;
    ULONG totalMatches = 0;
    SearchStats stats = {};
    BOOLEAN truncated = FALSE;

    for (ULONG i = 0; i < targetSnapshot.Count; ++i) {
        ++stats.EntriesScanned;
        status = SearchEntry(
            targetSnapshot.Entries[i],
            request.Pattern,
            request.PatternLength,
            response,
            capacity,
            &storedCount,
            &totalMatches,
            &stats,
            &truncated);

        if (!NT_SUCCESS(status) || truncated) {
            break;
        }
    }

    FreeSnapshot(&targetSnapshot);

    if (!NT_SUCCESS(status)) {
        return CompleteRequest(irp, status);
    }

    response->Count = storedCount;
    response->TotalMatches = totalMatches;
    response->Truncated = truncated ? 1 : 0;
    response->EntriesScanned = stats.EntriesScanned;
    response->EntriesSkipped = stats.EntriesSkipped;
    response->ReadFailures = stats.ReadFailures;
    response->Target = request.Target;

    return CompleteRequest(
        irp,
        STATUS_SUCCESS,
        headerSize + sizeof(KN_DIFF_POOL_SEARCH_RESULT) * storedCount);
}

NTSTATUS HandleClearSnapshots(PIRP irp, ULONG outputBufferLength)
{
    const ULONG clearedEntries = ClearSnapshots();
    return CompleteCountResponse(irp, outputBufferLength, clearedEntries);
}

NTSTATUS HandleAllocTestPool(PIRP irp, ULONG inputBufferLength, ULONG outputBufferLength)
{
    if (inputBufferLength < sizeof(KN_DIFF_POOL_TEST_ALLOC_REQUEST)) {
        return CompleteRequest(irp, STATUS_INVALID_PARAMETER);
    }
    if (outputBufferLength < sizeof(KN_DIFF_POOL_TEST_ALLOC_RESPONSE)) {
        return CompleteRequest(irp, STATUS_BUFFER_TOO_SMALL);
    }

    auto systemBuffer = irp->AssociatedIrp.SystemBuffer;
    if (systemBuffer == nullptr) {
        return CompleteRequest(irp, STATUS_INVALID_USER_BUFFER);
    }

    KN_DIFF_POOL_TEST_ALLOC_REQUEST request =
        *static_cast<PKN_DIFF_POOL_TEST_ALLOC_REQUEST>(systemBuffer);

    ULONG size = request.Size;
    if (size == 0) {
        size = KN_DIFF_POOL_DEFAULT_TEST_SIZE;
    }
    if (size < KN_DIFF_POOL_DEFAULT_TEST_SIZE) {
        size = KN_DIFF_POOL_DEFAULT_TEST_SIZE;
    }
    if (size > KN_DIFF_POOL_MAX_TEST_SIZE) {
        return CompleteRequest(irp, STATUS_INVALID_PARAMETER);
    }

    ULONG tag = request.Tag != 0 ? request.Tag : KN_DIFF_TEST_POOL_DEFAULT_TAG;
    ULONG fillLength = request.FillLength;
    const UCHAR* fill = request.Fill;

    if (fillLength == 0) {
        fill = KN_DIFF_TEST_POOL_DEFAULT_FILL;
        fillLength = sizeof(KN_DIFF_TEST_POOL_DEFAULT_FILL) - 1;
    }
    if (fillLength > KN_DIFF_POOL_MAX_TEST_FILL) {
        return CompleteRequest(irp, STATUS_INVALID_PARAMETER);
    }

    ExAcquireFastMutex(&g_TestAllocationLock);
    const BOOLEAN tooManyBeforeAllocation =
        g_TestAllocationCount >= KN_DIFF_POOL_MAX_TEST_ALLOCATIONS;
    ExReleaseFastMutex(&g_TestAllocationLock);

    if (tooManyBeforeAllocation) {
        return CompleteRequest(irp, STATUS_QUOTA_EXCEEDED);
    }

    PVOID allocationAddress = ExAllocatePool2(POOL_FLAG_NON_PAGED, size, tag);
    if (allocationAddress == nullptr) {
        return CompleteRequest(irp, STATUS_INSUFFICIENT_RESOURCES);
    }

    TestAllocation* allocation = static_cast<TestAllocation*>(
        ExAllocatePool2(POOL_FLAG_NON_PAGED, sizeof(TestAllocation), KN_DIFF_POOL_TAG));
    if (allocation == nullptr) {
        ExFreePoolWithTag(allocationAddress, tag);
        return CompleteRequest(irp, STATUS_INSUFFICIENT_RESOURCES);
    }

    RtlZeroMemory(allocation, sizeof(*allocation));
    allocation->Address = allocationAddress;
    allocation->Size = size;
    allocation->Tag = tag;
    FillTestAllocation(allocationAddress, size, fill, fillLength);

    ULONG activeAllocations = 0;
    ExAcquireFastMutex(&g_TestAllocationLock);
    if (g_TestAllocationCount >= KN_DIFF_POOL_MAX_TEST_ALLOCATIONS) {
        ExReleaseFastMutex(&g_TestAllocationLock);
        ExFreePoolWithTag(allocationAddress, tag);
        ExFreePoolWithTag(allocation, KN_DIFF_POOL_TAG);
        return CompleteRequest(irp, STATUS_QUOTA_EXCEEDED);
    }

    InsertTailList(&g_TestAllocations, &allocation->Link);
    ++g_TestAllocationCount;
    activeAllocations = g_TestAllocationCount;
    ExReleaseFastMutex(&g_TestAllocationLock);

    auto response = static_cast<PKN_DIFF_POOL_TEST_ALLOC_RESPONSE>(systemBuffer);
    RtlZeroMemory(response, sizeof(*response));
    response->Address = reinterpret_cast<unsigned __int64>(allocationAddress);
    response->Size = size;
    response->Tag = tag;
    response->ActiveAllocations = activeAllocations;

    return CompleteRequest(irp, STATUS_SUCCESS, sizeof(*response));
}

NTSTATUS HandleFreeTestPools(PIRP irp, ULONG outputBufferLength)
{
    const ULONG freed = FreeAllTestAllocations();
    ClearSnapshots();
    return CompleteCountResponse(irp, outputBufferLength, freed);
}

NTSTATUS HandleSetHashPolicy(PIRP irp, ULONG inputBufferLength)
{
    if (inputBufferLength < sizeof(KN_DIFF_POOL_HASH_POLICY_REQUEST)) {
        return CompleteRequest(irp, STATUS_INVALID_PARAMETER);
    }

    auto systemBuffer = irp->AssociatedIrp.SystemBuffer;
    if (systemBuffer == nullptr) {
        return CompleteRequest(irp, STATUS_INVALID_USER_BUFFER);
    }

    KN_DIFF_POOL_HASH_POLICY_REQUEST request =
        *static_cast<PKN_DIFF_POOL_HASH_POLICY_REQUEST>(systemBuffer);

    if (request.Mode != KN_DIFF_POOL_HASH_MODE_SAMPLE &&
        request.Mode != KN_DIFF_POOL_HASH_MODE_FULL) {
        return CompleteRequest(irp, STATUS_INVALID_PARAMETER);
    }

    unsigned __int64 sampleBytes = request.SampleBytes;
    if (sampleBytes == 0) {
        sampleBytes = KN_DIFF_POOL_HASH_SAMPLE_MAX;
    }
    if (sampleBytes < KN_DIFF_POOL_HASH_SAMPLE_MIN) {
        sampleBytes = KN_DIFF_POOL_HASH_SAMPLE_MIN;
    }
    if (sampleBytes > KN_DIFF_POOL_HASH_SAMPLE_LIMIT) {
        sampleBytes = KN_DIFF_POOL_HASH_SAMPLE_LIMIT;
    }

    ExAcquireFastMutex(&g_HashPolicyLock);
    g_HashMode = request.Mode;
    g_HashSampleBytes = sampleBytes;
    ExReleaseFastMutex(&g_HashPolicyLock);
    return CompleteRequest(irp, STATUS_SUCCESS);
}

NTSTATUS DispatchDeviceControl(PDEVICE_OBJECT deviceObject, PIRP irp)
{
    UNREFERENCED_PARAMETER(deviceObject);

    auto stack = IoGetCurrentIrpStackLocation(irp);
    const ULONG ioControlCode = stack->Parameters.DeviceIoControl.IoControlCode;
    const ULONG inputBufferLength = stack->Parameters.DeviceIoControl.InputBufferLength;
    const ULONG outputBufferLength = stack->Parameters.DeviceIoControl.OutputBufferLength;

    switch (ioControlCode) {
    case IOCTL_KN_DIFF_POOL_PING:
        return HandlePing(irp, outputBufferLength);
    case IOCTL_KN_DIFF_POOL_CREATE_BASELINE:
        return HandleCreateBaseline(irp, outputBufferLength);
    case IOCTL_KN_DIFF_POOL_CREATE_CURRENT:
        return HandleCreateCurrent(irp, outputBufferLength);
    case IOCTL_KN_DIFF_POOL_GET_DIFF_COUNT:
        return HandleGetDiffCount(irp, outputBufferLength);
    case IOCTL_KN_DIFF_POOL_GET_DIFF_ENTRIES:
        return HandleGetDiffEntries(irp, outputBufferLength);
    case IOCTL_KN_DIFF_POOL_READ_CONTENT:
        return HandleReadContent(irp, inputBufferLength, outputBufferLength);
    case IOCTL_KN_DIFF_POOL_SEARCH_CONTENT:
        return HandleSearchContent(irp, inputBufferLength, outputBufferLength);
    case IOCTL_KN_DIFF_POOL_CLEAR_SNAPSHOTS:
        return HandleClearSnapshots(irp, outputBufferLength);
    case IOCTL_KN_DIFF_POOL_ALLOC_TEST_POOL:
        return HandleAllocTestPool(irp, inputBufferLength, outputBufferLength);
    case IOCTL_KN_DIFF_POOL_FREE_TEST_POOLS:
        return HandleFreeTestPools(irp, outputBufferLength);
    case IOCTL_KN_DIFF_POOL_GET_CURRENT_COUNT:
        return HandleGetCurrentCount(irp, outputBufferLength);
    case IOCTL_KN_DIFF_POOL_GET_CURRENT_ENTRIES:
        return HandleGetCurrentEntries(irp, outputBufferLength);
    case IOCTL_KN_DIFF_POOL_GET_PTE_DETAILS:
        return HandleGetPteDetails(irp, inputBufferLength, outputBufferLength);
    case IOCTL_KN_DIFF_POOL_SET_HASH_POLICY:
        return HandleSetHashPolicy(irp, inputBufferLength);
    default:
        return CompleteRequest(irp, STATUS_INVALID_DEVICE_REQUEST);
    }
}
}

void DriverUnloadRoutine(PDRIVER_OBJECT driverObject)
{
    UNREFERENCED_PARAMETER(driverObject);

    FreeAllTestAllocations();

    UNICODE_STRING symbolicLinkName;
    RtlInitUnicodeString(&symbolicLinkName, KN_DIFF_POOL_SYMBOLIC_LINK_NAME);
    IoDeleteSymbolicLink(&symbolicLinkName);

    if (g_DeviceObject != nullptr) {
        IoDeleteDevice(g_DeviceObject);
        g_DeviceObject = nullptr;
    }

    ExAcquireFastMutex(&g_SnapshotLock);
    FreeSnapshot(&g_Baseline);
    FreeSnapshot(&g_Current);
    FreeSnapshot(&g_Diff);
    ExReleaseFastMutex(&g_SnapshotLock);
}

EXTERN_C NTSTATUS DriverEntry(PDRIVER_OBJECT driverObject, PUNICODE_STRING registryPath)
{
    UNREFERENCED_PARAMETER(registryPath);

    ExInitializeFastMutex(&g_SnapshotLock);
    ExInitializeFastMutex(&g_TestAllocationLock);
    ExInitializeFastMutex(&g_HashPolicyLock);
    InitializeListHead(&g_TestAllocations);

    UNICODE_STRING deviceName;
    RtlInitUnicodeString(&deviceName, KN_DIFF_POOL_DEVICE_NAME);

    PDEVICE_OBJECT deviceObject = nullptr;
    NTSTATUS status = IoCreateDeviceSecure(
        driverObject,
        0,
        &deviceName,
        FILE_DEVICE_KN_DIFF_POOL,
        FILE_DEVICE_SECURE_OPEN,
        FALSE,
        &SDDL_DEVOBJ_SYS_ALL_ADM_ALL,
        &KN_DIFF_POOL_DEVICE_CLASS_GUID,
        &deviceObject);

    if (!NT_SUCCESS(status)) {
        return status;
    }

    UNICODE_STRING symbolicLinkName;
    RtlInitUnicodeString(&symbolicLinkName, KN_DIFF_POOL_SYMBOLIC_LINK_NAME);

    status = IoCreateSymbolicLink(&symbolicLinkName, &deviceName);
    if (!NT_SUCCESS(status)) {
        IoDeleteDevice(deviceObject);
        return status;
    }

    for (ULONG i = 0; i <= IRP_MJ_MAXIMUM_FUNCTION; ++i) {
        driverObject->MajorFunction[i] = DispatchUnsupported;
    }

    driverObject->MajorFunction[IRP_MJ_CREATE] = DispatchCreateClose;
    driverObject->MajorFunction[IRP_MJ_CLOSE] = DispatchCreateClose;
    driverObject->MajorFunction[IRP_MJ_DEVICE_CONTROL] = DispatchDeviceControl;
    driverObject->DriverUnload = DriverUnloadRoutine;

    g_DeviceObject = deviceObject;
    deviceObject->Flags &= ~DO_DEVICE_INITIALIZING;

    return STATUS_SUCCESS;
}
