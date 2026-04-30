#include <ntddk.h>
#include <ntstrsafe.h>

#define TAG_SCAN 'nacS'
#define TAG_TARGET 'SG'


#pragma warning (disable : 4201)
#pragma warning (disable : 4996)

using FN_ZwQuerySystemInformation = NTSTATUS(NTAPI*)(
	_In_      ULONG		SystemInformationClass,
	_Inout_   PVOID                    SystemInformation,
	_In_      ULONG                    SystemInformationLength,
	_Out_opt_ PULONG                   ReturnLength
	);

FN_ZwQuerySystemInformation g_pfnZwQuerySystemInformation = nullptr;

NTSTATUS TestPTE();
void CheckAddressByPTE(PVOID targetAddress);;

typedef enum _SYSTEM_INFORMATION_CLASS {
	SystemBigPoolInformation = 0x42
} SYSTEM_INFORMATION_CLASS;

typedef struct _SYSTEM_BIGPOOL_ENTRY
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

} SYSTEM_BIGPOOL_ENTRY, * PSYSTEM_BIGPOOL_ENTRY;


typedef struct _SYSTEM_BIGPOOL_INFORMATION {
	ULONG Count;
	SYSTEM_BIGPOOL_ENTRY Entries[1];
} SYSTEM_BIGPOOL_INFORMATION, * PSYSTEM_BIGPOOL_INFORMATION;

/*
NTSYSAPI NTSTATUS NTAPI ZwQuerySystemInformation(
	IN SYSTEM_INFORMATION_CLASS SystemInformationClass,
	OUT PVOID SystemInformation,
	IN ULONG SystemInformationLength,
	OUT PULONG ReturnLength OPTIONAL
);
*/

#define PAGE_EXECUTE_FLAGS (PAGE_EXECUTE | PAGE_EXECUTE_READ | PAGE_EXECUTE_READWRITE | PAGE_EXECUTE_WRITECOPY)
#define PAGE_WRITE_FLAGS   (PAGE_READWRITE | PAGE_EXECUTE_READWRITE | PAGE_WRITECOPY | PAGE_EXECUTE_WRITECOPY)

BOOLEAN IsMemoryAccessible(PVOID address, SIZE_T size, BOOLEAN* hasWrite, BOOLEAN* hasExecute)
{
	PMDL mdl = IoAllocateMdl(address, (ULONG)size, FALSE, FALSE, NULL);
	if (!mdl) return FALSE;

	__try {
		MmProbeAndLockPages(mdl, KernelMode, IoReadAccess);
	}
	__except (EXCEPTION_EXECUTE_HANDLER) {
		IoFreeMdl(mdl);
		return FALSE;
	}

	*hasWrite = FALSE;
	*hasExecute = FALSE;

	/*
	PPFN_NUMBER pfns = MmGetMdlPfnArray(mdl);
	ULONG pageCount = ADDRESS_AND_SIZE_TO_SPAN_PAGES(address, size);

	for (ULONG i = 0; i < pageCount; ++i) {
		PHYSICAL_ADDRESS pa = { 0 };
		pa.QuadPart = (LONGLONG)pfns[i] << PAGE_SHIFT;

		PVOID va = MmGetVirtualForPhysical(pa);
		if (!va) continue;

		ULONG protect;
		if (MmIsNonPagedSystemAddressValid(va)) {
			protect = MmGetPageProtect(va); // 비공식 함수, 아래 방식 대체 가능
			if (protect & PAGE_WRITE_FLAGS) *hasWrite = TRUE;
			if (protect & PAGE_EXECUTE_FLAGS) *hasExecute = TRUE;
		}
	}
	*/
	MmUnlockPages(mdl);
	IoFreeMdl(mdl);
	return TRUE;
}

// ZwQuerySystemInformation 호출
NTSTATUS QuerySystemBigPoolInfo(PSYSTEM_BIGPOOL_INFORMATION* OutBuffer, PULONG OutSize)
{
	NTSTATUS status = STATUS_SUCCESS;
	ULONG bufferSize = 0x10000;
	PVOID buffer = NULL;

	do {
		if (buffer)
			ExFreePool(buffer);

		buffer = ExAllocatePoolWithTag(NonPagedPoolNx, bufferSize, TAG_SCAN);
		if (!buffer)
			return STATUS_INSUFFICIENT_RESOURCES;

		status = g_pfnZwQuerySystemInformation(SystemBigPoolInformation, buffer, bufferSize, OutSize);
		if (status == STATUS_INFO_LENGTH_MISMATCH) {
			bufferSize *= 2;
		}
	} while (status == STATUS_INFO_LENGTH_MISMATCH);

	if (!NT_SUCCESS(status)) {
		ExFreePool(buffer);
		return status;
	}

	*OutBuffer = (PSYSTEM_BIGPOOL_INFORMATION)buffer;
	return STATUS_SUCCESS;
}

// NonPaged Pool 스캔
VOID ScanNonPagedPool()
{
	PSYSTEM_BIGPOOL_INFORMATION bigPoolInfo = NULL;
	ULONG actualSize = 0;
	NTSTATUS status = QuerySystemBigPoolInfo(&bigPoolInfo, &actualSize);

	if (!NT_SUCCESS(status)) {
		DbgPrint("[PoolScan] Failed to query big pool info: 0x%X\n", status);
		return;
	}

	for (ULONG i = 0; i < bigPoolInfo->Count; i++) {
		SYSTEM_BIGPOOL_ENTRY* entry = &bigPoolInfo->Entries[i];

		if (!entry->NonPaged) continue;

		PVOID va = (PVOID)((ULONG_PTR)entry->VirtualAddress & ~1ULL);
		SIZE_T size = entry->SizeInBytes;
		CHAR tag[5] = { 0 };
		RtlCopyMemory(tag, entry->Tag, 4);

		BOOLEAN writable = FALSE, executable = FALSE;
		BOOLEAN accessible = IsMemoryAccessible(va, size, &writable, &executable);

		/*
		DbgPrint("[PoolScan] Tag: %.4s, Addr: %p, Size: 0x%Ix, Access: %s\n",
			tag,
			va,
			size,
			accessible ? "Yes" : "No"
		);
		*/
		USHORT targetTag = TAG_TARGET;
		if (RtlCompareMemory(tag, &targetTag, sizeof(targetTag)) == sizeof(targetTag))
		{
			DbgPrint("[PoolScan] >>>>>>>>>>>>>>>>>>> FOUND\n");
			DbgPrint("[PoolScan] Tag: %.4s, Addr: %p, Size: 0x%Ix, Access: %s\n",
				tag,
				va,
				size,
				accessible ? "Yes" : "No"
			);

			if (accessible && size >= 16)
			{
				CheckAddressByPTE(va);

				PUCHAR data = (PUCHAR)va;
				DbgPrint("[PoolScan] DATA >>> %02X %02X %02X %02X %02X %02X %02X %02X\n", data[0], data[1], data[2], data[3], data[4], data[5], data[6], data[7]);
			}
		}
	}

	ExFreePool(bigPoolInfo);
}

PVOID targetMem = nullptr;

VOID DriverUnload(PDRIVER_OBJECT DriverObject)
{
	UNREFERENCED_PARAMETER(DriverObject);
	DbgPrint("[PoolScan] Driver unloaded.\n");

	if (targetMem)
	{
		ExFreePoolWithTag(targetMem, TAG_TARGET);
		targetMem = nullptr;
	}
}

void foo()
{
	TestPTE();
}

extern "C" NTSTATUS DriverEntry(PDRIVER_OBJECT DriverObject, PUNICODE_STRING RegistryPath)
{
	UNREFERENCED_PARAMETER(RegistryPath);
	DriverObject->DriverUnload = DriverUnload;

	//foo();
	//return STATUS_SUCCESS;

	{
		targetMem = ExAllocatePoolWithTag(NonPagedPoolNx, 0x18000, TAG_TARGET);
		DbgPrint("[PoolScan] targetMem : %p\n", targetMem);
	}
	
	{
		UNICODE_STRING funcName = RTL_CONSTANT_STRING(L"ZwQuerySystemInformation");
		g_pfnZwQuerySystemInformation = (FN_ZwQuerySystemInformation)MmGetSystemRoutineAddress(&funcName);
	}

	DbgPrint("[PoolScan] Driver loaded. Scanning NonPaged Pool...\n");

	ScanNonPagedPool();

	return STATUS_SUCCESS;
}
