package singleinstance

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

const DefaultMutexName = `Local\KN_DIFF_POOL_USERMODE_SINGLE_INSTANCE`

var ErrAlreadyRunning = errors.New("another kn-pool-diff instance is already running")

type Lock struct {
	handle windows.Handle
}

func Acquire(name string) (*Lock, error) {
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("invalid mutex name %q: %w", name, err)
	}

	handle, err := windows.CreateMutex(nil, false, ptr)
	if handle == 0 {
		return nil, fmt.Errorf("create mutex %q: %w", name, err)
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(handle)
		return nil, ErrAlreadyRunning
	}
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("create mutex %q: %w", name, err)
	}

	return &Lock{handle: handle}, nil
}

func (lock *Lock) Close() error {
	if lock == nil || lock.handle == 0 {
		return nil
	}
	handle := lock.handle
	lock.handle = 0
	return windows.CloseHandle(handle)
}
