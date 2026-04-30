package main

import (
	"errors"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"kn-diff-pool/kn-pool-diff/internal/device"
	"kn-diff-pool/kn-pool-diff/internal/privilege"
	"kn-diff-pool/kn-pool-diff/internal/scm"
	"kn-diff-pool/kn-pool-diff/internal/singleinstance"
	"kn-diff-pool/kn-pool-diff/internal/tui"
)

func main() {
	exitCode := 0
	lock, err := singleinstance.Acquire(singleinstance.DefaultMutexName)
	if errors.Is(err, singleinstance.ErrAlreadyRunning) {
		fmt.Fprintln(os.Stderr, "kn-pool-diff is already running.")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "kn-pool-diff single-instance check failed: %v\n", err)
		os.Exit(1)
	}
	defer lock.Close()

	program := tea.NewProgram(tui.NewModel(), tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "kn-pool-diff failed: %v\n", err)
		exitCode = 1
	}

	if err := cleanupDriver(); err != nil {
		fmt.Fprintf(os.Stderr, "kn-pool-diff cleanup failed: %v\n", err)
		exitCode = 1
	}

	os.Exit(exitCode)
}

func cleanupDriver() error {
	admin := privilege.Query()
	if admin.Err != nil || !admin.Elevated {
		return nil
	}

	return device.RunWithoutOpenHandles(func() error {
		_, err := scm.StopAndDelete()
		return err
	})
}
