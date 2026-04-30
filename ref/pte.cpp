#include <ntddk.h>
#include <intrin.h>

#define GET_PML4_INDEX(va) ((((ULONG64)(va)) >> 39) & 0x1FF)
#define GET_PDPT_INDEX(va) ((((ULONG64)(va)) >> 30) & 0x1FF)
#define GET_PDT_INDEX(va)  ((((ULONG64)(va)) >> 21) & 0x1FF)
#define GET_PT_INDEX(va)   ((((ULONG64)(va)) >> 12) & 0x1FF)

typedef struct _HARDWARE_PTE {
	ULONG64 Valid : 1;             // [0] 유효 비트 (Present)
	ULONG64 Write : 1;             // [1] 쓰기 가능 여부 (Read/Write)
	ULONG64 Owner : 1;             // [2] 접근 권한 (User/Supervisor)
	ULONG64 WriteThrough : 1;      // [3] 쓰기 정책 (Write-Through)
	ULONG64 CacheDisable : 1;      // [4] 캐시 비활성화 (Cache-Disable)
	ULONG64 Accessed : 1;          // [5] 접근 여부 (Accessed)
	ULONG64 Dirty : 1;             // [6] 페이지 변경 여부 (Dirty)
	ULONG64 LargePage : 1;         // [7] Large Page(2MB/1GB) 여부
	ULONG64 Global : 1;            // [8] 전역 페이지 여부 (Global)
	ULONG64 CopyOnWrite : 1;       // [9] (소프트웨어용)
	ULONG64 Prototype : 1;         // [10] (소프트웨어용)
	ULONG64 reserved0 : 1;         // [11] (소프트웨어용)
	ULONG64 PageFrameNumber : 36;  // [12-47] 물리 페이지 프레임 번호
	ULONG64 reserved1 : 4;         // [48-51] 예약됨
	ULONG64 SoftwareWsIndex : 11;  // [52-62] (소프트웨어용)
	ULONG64 NoExecute : 1;         // [63] 실행 금지 비트 (Execute Disable)
} HARDWARE_PTE, * PHARDWARE_PTE;

#include <ntddk.h>
#include <intrin.h>

// MmCopyMemory의 숨겨진 기능을 사용하기 위한 선언입니다.
// 이 구조체는 지정된 물리 주소에서 메모리를 복사하는 데 사용됩니다.
/*
typedef struct _MM_COPY_ADDRESS {
	union {
		PVOID VirtualAddress;
		PHYSICAL_ADDRESS PhysicalAddress;
	};
} MM_COPY_ADDRESS, * PMM_COPY_ADDRESS;
*/
// MmCopyMemory는 공식적으로 문서화되지 않은 플래그를 가지고 있습니다.
// MM_COPY_MEMORY_PHYSICAL 플래그는 소스 주소가 물리 주소임을 나타냅니다.
#define MM_COPY_MEMORY_PHYSICAL 0x1

/*
// 외부 함수 선언
NTSTATUS MmCopyMemory(
	_In_ PVOID TargetAddress,
	_In_ MM_COPY_ADDRESS SourceAddress,
	_In_ SIZE_T NumberOfBytes,
	_In_ ULONG Flags,
	_Out_ PSIZE_T NumberOfBytesTransferred
);
*/


NTSTATUS CheckAddressExecutable(
	_In_ PVOID VirtualAddress,
	_Out_ PBOOLEAN IsExecutable,
	PBOOLEAN IsWritable
)
{
	if (IsExecutable == NULL) {
		return STATUS_INVALID_PARAMETER;
	}

	*IsExecutable = FALSE;
	*IsWritable = FALSE;

	BOOLEAN isCurrentlyWritable = TRUE;

	// 각 페이지 테이블을 저장할 로컬 버퍼 (페이지 크기)
	// 512개의 8바이트 엔트리 = 4096 바이트
	HARDWARE_PTE pageTableBuffer[512];
	SIZE_T bytesCopied = 0;
	NTSTATUS status = STATUS_SUCCESS;

	// 1. CR3에서 PML4 테이블의 물리 주소를 가져옵니다.
	MM_COPY_ADDRESS sourceAddress = { 0 };
	sourceAddress.PhysicalAddress.QuadPart = __readcr3();

	// 2. PML4 테이블을 매핑 대신 로컬 버퍼로 복사합니다.
	status = MmCopyMemory(pageTableBuffer, sourceAddress, PAGE_SIZE, MM_COPY_MEMORY_PHYSICAL, &bytesCopied);
	if (!NT_SUCCESS(status) || bytesCopied != PAGE_SIZE) {
		KdPrintEx((DPFLTR_IHVDRIVER_ID, DPFLTR_ERROR_LEVEL, "CheckAddressExecutableV2: Failed to copy PML4 table. Status: 0x%X\n", status));
		return status;
	}

	// 3. 인덱스 계산
	ULONG64 pml4Index = GET_PML4_INDEX(VirtualAddress);
	ULONG64 pdptIndex = GET_PDPT_INDEX(VirtualAddress);
	ULONG64 pdtIndex = GET_PDT_INDEX(VirtualAddress);
	ULONG64 ptIndex = GET_PT_INDEX(VirtualAddress);

	// 4. PML4 엔트리 확인
	if (!pageTableBuffer[pml4Index].Valid) return STATUS_INVALID_ADDRESS;
	sourceAddress.PhysicalAddress.QuadPart = pageTableBuffer[pml4Index].PageFrameNumber << PAGE_SHIFT;

	// 5. PDPT 복사 및 확인
	status = MmCopyMemory(pageTableBuffer, sourceAddress, PAGE_SIZE, MM_COPY_MEMORY_PHYSICAL, &bytesCopied);
	if (!NT_SUCCESS(status) || bytesCopied != PAGE_SIZE) return status;

	if (!pageTableBuffer[pdptIndex].Valid) return STATUS_INVALID_ADDRESS;
	if (pageTableBuffer[pdptIndex].LargePage) {
		*IsExecutable = (pageTableBuffer[pdptIndex].NoExecute == 0);
		*IsWritable = (pageTableBuffer[pdptIndex].Write == TRUE);
		return STATUS_SUCCESS;
	}
	sourceAddress.PhysicalAddress.QuadPart = pageTableBuffer[pdptIndex].PageFrameNumber << PAGE_SHIFT;

	// 6. PDT 복사 및 확인
	status = MmCopyMemory(pageTableBuffer, sourceAddress, PAGE_SIZE, MM_COPY_MEMORY_PHYSICAL, &bytesCopied);
	if (!NT_SUCCESS(status) || bytesCopied != PAGE_SIZE) return status;

	if (!pageTableBuffer[pdtIndex].Valid) return STATUS_INVALID_ADDRESS;
	if (pageTableBuffer[pdtIndex].LargePage) {
		*IsExecutable = (pageTableBuffer[pdtIndex].NoExecute == 0);
		*IsWritable = (pageTableBuffer[pdptIndex].Write == TRUE);
		return STATUS_SUCCESS;
	}
	sourceAddress.PhysicalAddress.QuadPart = pageTableBuffer[pdtIndex].PageFrameNumber << PAGE_SHIFT;

	// 7. PT 복사 및 확인
	status = MmCopyMemory(pageTableBuffer, sourceAddress, PAGE_SIZE, MM_COPY_MEMORY_PHYSICAL, &bytesCopied);
	if (!NT_SUCCESS(status) || bytesCopied != PAGE_SIZE) return status;

	if (!pageTableBuffer[ptIndex].Valid) return STATUS_INVALID_ADDRESS;

	// 8. 최종 PTE에서 실행 속성 확인
	*IsExecutable = (pageTableBuffer[ptIndex].NoExecute == 0);
	*IsWritable = (pageTableBuffer[pdptIndex].Write == TRUE);

	return STATUS_SUCCESS;
}

NTSTATUS CheckAddressExecutable2(
	_In_ PVOID VirtualAddress,
	_Out_ PBOOLEAN IsExecutable,
	_Out_ PBOOLEAN IsWritable
)
{
	if (IsExecutable == NULL || IsWritable == NULL) {
		return STATUS_INVALID_PARAMETER;
	}

	*IsExecutable = FALSE;
	*IsWritable = FALSE;

	BOOLEAN isCurrentlyExecutable = TRUE;
	BOOLEAN isCurrentlyWritable = TRUE;

	HARDWARE_PTE pageTableBuffer[512];
	SIZE_T bytesCopied = 0;
	NTSTATUS status = STATUS_SUCCESS;
	MM_COPY_ADDRESS sourceAddress = { 0 };
	sourceAddress.PhysicalAddress.QuadPart = __readcr3();

	status = MmCopyMemory(pageTableBuffer, sourceAddress, PAGE_SIZE, MM_COPY_MEMORY_PHYSICAL, &bytesCopied);
	if (!NT_SUCCESS(status) || bytesCopied != PAGE_SIZE) return status;

	ULONG64 pml4Index = GET_PML4_INDEX(VirtualAddress);
	if (!pageTableBuffer[pml4Index].Valid) return STATUS_INVALID_ADDRESS;

	isCurrentlyWritable &= (pageTableBuffer[pml4Index].Write == 1);
	isCurrentlyExecutable &= (pageTableBuffer[pml4Index].NoExecute == 0);

	sourceAddress.PhysicalAddress.QuadPart = pageTableBuffer[pml4Index].PageFrameNumber << PAGE_SHIFT;

	status = MmCopyMemory(pageTableBuffer, sourceAddress, PAGE_SIZE, MM_COPY_MEMORY_PHYSICAL, &bytesCopied);
	if (!NT_SUCCESS(status) || bytesCopied != PAGE_SIZE) return status;

	ULONG64 pdptIndex = GET_PDPT_INDEX(VirtualAddress);
	if (!pageTableBuffer[pdptIndex].Valid) return STATUS_INVALID_ADDRESS;

	isCurrentlyWritable &= (pageTableBuffer[pdptIndex].Write == 1);
	isCurrentlyExecutable &= (pageTableBuffer[pdptIndex].NoExecute == 0);

	if (pageTableBuffer[pdptIndex].LargePage) { // 1GB Large Page
		*IsWritable = isCurrentlyWritable;
		*IsExecutable = isCurrentlyExecutable;
		return STATUS_SUCCESS;
	}
	sourceAddress.PhysicalAddress.QuadPart = pageTableBuffer[pdptIndex].PageFrameNumber << PAGE_SHIFT;

	// pdt
	status = MmCopyMemory(pageTableBuffer, sourceAddress, PAGE_SIZE, MM_COPY_MEMORY_PHYSICAL, &bytesCopied);
	if (!NT_SUCCESS(status) || bytesCopied != PAGE_SIZE) return status;

	ULONG64 pdtIndex = GET_PDT_INDEX(VirtualAddress);
	if (!pageTableBuffer[pdtIndex].Valid) return STATUS_INVALID_ADDRESS;

	isCurrentlyWritable &= (pageTableBuffer[pdtIndex].Write == 1);
	isCurrentlyExecutable &= (pageTableBuffer[pdtIndex].NoExecute == 0);

	if (pageTableBuffer[pdtIndex].LargePage) { // 2MB Large Page
		*IsWritable = isCurrentlyWritable;
		*IsExecutable = isCurrentlyExecutable;
		return STATUS_SUCCESS;
	}
	sourceAddress.PhysicalAddress.QuadPart = pageTableBuffer[pdtIndex].PageFrameNumber << PAGE_SHIFT;

	// pt
	status = MmCopyMemory(pageTableBuffer, sourceAddress, PAGE_SIZE, MM_COPY_MEMORY_PHYSICAL, &bytesCopied);
	if (!NT_SUCCESS(status) || bytesCopied != PAGE_SIZE) return status;

	ULONG64 ptIndex = GET_PT_INDEX(VirtualAddress);
	if (!pageTableBuffer[ptIndex].Valid) return STATUS_INVALID_ADDRESS;

	isCurrentlyWritable &= (pageTableBuffer[ptIndex].Write == 1);
	isCurrentlyExecutable &= (pageTableBuffer[ptIndex].NoExecute == 0);

	// final
	*IsWritable = isCurrentlyWritable;
	*IsExecutable = isCurrentlyExecutable;

	return STATUS_SUCCESS;
}

BOOLEAN CheckIfPml5IsEnabled()
{
	// --- 1. CPU가 5단계 페이징을 하드웨어적으로 지원하는지 확인 ---

	int cpuInfo[4] = { 0 }; // EAX, EBX, ECX, EDX 결과를 저장할 배열

	// CPUID.(EAX=7, ECX=0) -> ECX[16] 비트가 5-level paging 지원 여부.
	__cpuidex(cpuInfo, 7, 0);

	// ECX 레지스터는 cpuInfo[2]에 해당. 16번 비트를 확인.
	const ULONG32 pml5SupportBit = (1 << 16);
	if ((cpuInfo[2] & pml5SupportBit) == 0)
	{
		KdPrintEx((DPFLTR_IHVDRIVER_ID, DPFLTR_INFO_LEVEL, "[PoolScan] PML5 Check: CPU does not support 5-level paging.\n"));
		return FALSE;
	}

	KdPrintEx((DPFLTR_IHVDRIVER_ID, DPFLTR_INFO_LEVEL, "[PoolScan] PML5 Check: CPU supports 5-level paging.\n"));

	// --- 2. 운영체제가 5단계 페이징을 활성화했는지 확인 ---

	// CR4 컨트롤 레지스터를 읽어옴.
	ULONG_PTR cr4Value = __readcr4();

	// CR4의 12번 비트(LA57)가 1이면 OS에서 5단계 페이징이 활성화된 것.
	const ULONG64 la57EnableBit = (1ULL << 12);
	if ((cr4Value & la57EnableBit) == 0)
	{
		KdPrintEx((DPFLTR_IHVDRIVER_ID, DPFLTR_INFO_LEVEL, "[PoolScan] PML5 Check: OS has not enabled 5-level paging (CR4.LA57 is 0).\n"));
		return FALSE;
	}

	KdPrintEx((DPFLTR_IHVDRIVER_ID, DPFLTR_INFO_LEVEL, "[PoolScan] PML5 Check: OS has enabled 5-level paging (CR4.LA57 is 1).\n"));

	return TRUE;
}

void CheckAddressByPTE(PVOID targetAddress)
{
	if (targetAddress)
	{
		BOOLEAN isExecutable = FALSE;
		BOOLEAN isWritable = FALSE;

		NTSTATUS status = CheckAddressExecutable2(targetAddress, &isExecutable, &isWritable);

		if (NT_SUCCESS(status))
		{
			KdPrintEx((
				DPFLTR_IHVDRIVER_ID,
				DPFLTR_INFO_LEVEL,
				"[PoolScan] CheckAddressByPTE: Address 0x%p is %s %s\n",
				targetAddress,
				isExecutable ? "EXECUTABLE" : "NON-EXECUTABLE",
				isWritable ? "WRITABLE" : "NON-WRITABLE"
				));
		}
		else
		{
			KdPrintEx((
				DPFLTR_IHVDRIVER_ID,
				DPFLTR_ERROR_LEVEL,
				"[PoolScan] CheckAddressByPTE: Failed to check address 0x%p with status 0x%X\n",
				targetAddress,
				status
				));
		}
	}
}

NTSTATUS TestPTE()
{
	DbgPrint("CheckIfPml5IsEnabled : %d\n", CheckIfPml5IsEnabled());

    UNICODE_STRING funcName = RTL_CONSTANT_STRING(L"NtCreateFile");
    PVOID targetAddress = MmGetSystemRoutineAddress(&funcName);

    if (targetAddress)
    {
        BOOLEAN isExecutable = FALSE;
		BOOLEAN isWritable = FALSE;

        NTSTATUS status = CheckAddressExecutable2(targetAddress, &isExecutable, &isWritable);

        if (NT_SUCCESS(status))
        {
            KdPrintEx((
                DPFLTR_IHVDRIVER_ID,
                DPFLTR_INFO_LEVEL,
                "CheckAddressExecutable: Address 0x%p is %s %s\n",
                targetAddress,
                isExecutable ? "EXECUTABLE" : "NON-EXECUTABLE",
				isWritable ? "WRITABLE" : "NON-WRITABLE"
                ));
        }
        else
        {
            KdPrintEx((
                DPFLTR_IHVDRIVER_ID,
                DPFLTR_ERROR_LEVEL,
                "CheckAddressExecutable: Failed to check address 0x%p with status 0x%X\n",
                targetAddress,
                status
                ));
        }
    }
    else
    {
        KdPrintEx((DPFLTR_IHVDRIVER_ID, DPFLTR_ERROR_LEVEL, "CheckAddressExecutable: Could not find NtCreateFile address\n"));
    }


	{
		PVOID targetAddress = ExAllocatePoolWithTag(NonPagedPoolNx, 0x18000, 'TEST');
		
		BOOLEAN isExecutable = FALSE;
		BOOLEAN isWritable = FALSE;

		NTSTATUS status = CheckAddressExecutable2(targetAddress, &isExecutable, &isWritable);

		if (NT_SUCCESS(status))
		{
			KdPrintEx((
				DPFLTR_IHVDRIVER_ID,
				DPFLTR_INFO_LEVEL,
				"CheckAddressExecutable: Address 0x%p is %s %s\n",
				targetAddress,
				isExecutable ? "EXECUTABLE" : "NON-EXECUTABLE",
				isWritable ? "WRITABLE" : "NON-WRITABLE"
				));
		}
		else
		{
			KdPrintEx((
				DPFLTR_IHVDRIVER_ID,
				DPFLTR_ERROR_LEVEL,
				"CheckAddressExecutable: Failed to check address 0x%p with status 0x%X\n",
				targetAddress,
				status
				));
		}

		ExFreePoolWithTag(targetAddress, 'TEST');
	}
    return STATUS_SUCCESS;
}