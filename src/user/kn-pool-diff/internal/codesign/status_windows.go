package codesign

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	systemCodeIntegrityInformationClass = 103
	codeIntegrityOptionTestSign         = 0x02
)

type Status struct {
	TestSigning bool
	Options     uint32
	Err         error
}

type systemCodeIntegrityInformation struct {
	Length               uint32
	CodeIntegrityOptions uint32
}

var procNtQuerySystemInformation = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQuerySystemInformation")

func Query() Status {
	info := systemCodeIntegrityInformation{
		Length: uint32(unsafe.Sizeof(systemCodeIntegrityInformation{})),
	}

	status, _, _ := procNtQuerySystemInformation.Call(
		uintptr(systemCodeIntegrityInformationClass),
		uintptr(unsafe.Pointer(&info)),
		unsafe.Sizeof(info),
		0)
	if status != 0 {
		return Status{Err: fmt.Errorf("NtQuerySystemInformation(SystemCodeIntegrityInformation) failed: NTSTATUS 0x%08X", uint32(status))}
	}

	return Status{
		TestSigning: info.CodeIntegrityOptions&codeIntegrityOptionTestSign != 0,
		Options:     info.CodeIntegrityOptions,
	}
}
