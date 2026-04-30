package singleinstance

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestAcquireRejectsDuplicateInstance(t *testing.T) {
	name := fmt.Sprintf(`Local\KN_DIFF_POOL_TEST_%d`, time.Now().UnixNano())

	first, err := Acquire(name)
	if err != nil {
		t.Fatalf("first Acquire returned error: %v", err)
	}
	defer first.Close()

	second, err := Acquire(name)
	if !errors.Is(err, ErrAlreadyRunning) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("second Acquire error mismatch: got %v, want %v", err, ErrAlreadyRunning)
	}
	if second != nil {
		t.Fatalf("second Acquire returned a lock")
	}

	if err := first.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	third, err := Acquire(name)
	if err != nil {
		t.Fatalf("third Acquire after Close returned error: %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatalf("third Close returned error: %v", err)
	}
}
