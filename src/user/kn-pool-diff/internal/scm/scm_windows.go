package scm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"kn-diff-pool/kn-pool-diff/internal/protocol"
)

const (
	serviceKernelDriver = uint32(0x00000001)
	serviceDemandStart  = uint32(0x00000003)
	serviceErrorNormal  = uint32(0x00000001)
)

const (
	errServiceDoesNotExist   = syscall.Errno(1060)
	errServiceAlreadyRunning = syscall.Errno(1056)
	errServiceNotActive      = syscall.Errno(1062)
	errServiceMarkedDelete   = syscall.Errno(1072)
)

type DriverStatus struct {
	Installed bool
	State     svc.State
	StateText string
}

type UnloadResult struct {
	Installed  bool
	WasRunning bool
	Deleted    bool
	FinalState string
}

func Query() (DriverStatus, error) {
	manager, err := mgr.Connect()
	if err != nil {
		return DriverStatus{}, fmt.Errorf("connect service manager: %w", err)
	}
	defer manager.Disconnect()

	service, err := manager.OpenService(protocol.ServiceName)
	if isNotInstalled(err) || isMarkedForDelete(err) {
		return DriverStatus{Installed: false, StateText: "not installed"}, nil
	}
	if err != nil {
		return DriverStatus{}, fmt.Errorf("open service %s: %w", protocol.ServiceName, err)
	}
	defer service.Close()

	status, err := service.Query()
	if err != nil {
		return DriverStatus{}, fmt.Errorf("query service %s: %w", protocol.ServiceName, err)
	}

	return DriverStatus{
		Installed: true,
		State:     status.State,
		StateText: stateText(status.State),
	}, nil
}

func Install(driverPath string) error {
	resolved, err := filepath.Abs(driverPath)
	if err != nil {
		return fmt.Errorf("resolve driver path %q: %w", driverPath, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("driver path %q is not usable: %w", resolved, err)
	}
	if info.IsDir() {
		return fmt.Errorf("driver path %q is a directory", resolved)
	}

	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager: %w", err)
	}
	defer manager.Disconnect()

	existing, err := manager.OpenService(protocol.ServiceName)
	if err == nil {
		defer existing.Close()

		config, err := existing.Config()
		if err != nil {
			return fmt.Errorf("query existing service %s config: %w", protocol.ServiceName, err)
		}
		config.BinaryPathName = syscall.EscapeArg(resolved)
		config.DisplayName = protocol.ServiceDisplay
		config.ServiceType = serviceKernelDriver
		config.StartType = serviceDemandStart
		config.ErrorControl = serviceErrorNormal
		if err := existing.UpdateConfig(config); err != nil {
			return fmt.Errorf("update existing service %s driver path to %q: %w", protocol.ServiceName, resolved, err)
		}
		return nil
	}
	if !isNotInstalled(err) {
		return fmt.Errorf("open existing service %s: %w", protocol.ServiceName, err)
	}

	service, err := manager.CreateService(protocol.ServiceName, resolved, mgr.Config{
		DisplayName:  protocol.ServiceDisplay,
		ServiceType:  serviceKernelDriver,
		StartType:    serviceDemandStart,
		ErrorControl: serviceErrorNormal,
	})
	if err != nil {
		return fmt.Errorf("create kernel driver service %s for %q: %w", protocol.ServiceName, resolved, err)
	}
	defer service.Close()

	return nil
}

func Start() error {
	service, closeService, err := openService()
	if err != nil {
		return err
	}
	defer closeService()

	if err := service.Start(); err != nil && !errors.Is(err, errServiceAlreadyRunning) {
		return fmt.Errorf("start service %s: %w", protocol.ServiceName, err)
	}
	return waitForState(service, svc.Running, 5*time.Second)
}

func Stop() error {
	service, closeService, err := openService()
	if err != nil {
		return err
	}
	defer closeService()

	status, err := service.Query()
	if err != nil {
		return fmt.Errorf("query service %s before stop: %w", protocol.ServiceName, err)
	}
	if status.State == svc.Stopped {
		return nil
	}

	if _, err := service.Control(svc.Stop); err != nil && !errors.Is(err, errServiceNotActive) {
		return fmt.Errorf("stop service %s: %w", protocol.ServiceName, err)
	}
	return waitForState(service, svc.Stopped, 5*time.Second)
}

func UnloadIfRunning() (UnloadResult, error) {
	status, err := Query()
	if err != nil {
		return UnloadResult{}, err
	}

	result := UnloadResult{
		Installed:  status.Installed,
		FinalState: status.StateText,
	}
	if !status.Installed || status.State == svc.Stopped {
		return result, nil
	}

	result.WasRunning = true
	if err := Stop(); err != nil {
		return result, err
	}

	finalStatus, err := Query()
	if err != nil {
		return result, err
	}
	result.FinalState = finalStatus.StateText
	return result, nil
}

func StopAndDelete() (UnloadResult, error) {
	status, err := Query()
	if err != nil {
		return UnloadResult{}, err
	}

	result := UnloadResult{
		Installed:  status.Installed,
		WasRunning: status.Installed && status.State != svc.Stopped,
		FinalState: status.StateText,
	}
	if !status.Installed {
		return result, nil
	}

	if err := Delete(); err != nil {
		return result, err
	}

	result.Deleted = true
	result.FinalState = "deleted"
	return result, nil
}

func Delete() error {
	if err := Stop(); err != nil {
		if isNotInstalled(err) || isMarkedForDelete(err) {
			return nil
		}
		return fmt.Errorf("stop service %s before delete: %w", protocol.ServiceName, err)
	}

	service, closeService, err := openService()
	if isNotInstalled(err) || isMarkedForDelete(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open service %s for delete: %w", protocol.ServiceName, err)
	}
	defer closeService()

	if err := service.Delete(); err != nil && !isMarkedForDelete(err) {
		return fmt.Errorf("delete service %s: %w", protocol.ServiceName, err)
	}
	return nil
}

func openService() (*mgr.Service, func(), error) {
	manager, err := mgr.Connect()
	if err != nil {
		return nil, nil, fmt.Errorf("connect service manager: %w", err)
	}

	service, err := manager.OpenService(protocol.ServiceName)
	if err != nil {
		manager.Disconnect()
		return nil, nil, fmt.Errorf("open service %s: %w", protocol.ServiceName, err)
	}

	return service, func() {
		service.Close()
		manager.Disconnect()
	}, nil
}

func waitForState(service *mgr.Service, desired svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for {
		status, err := service.Query()
		if err != nil {
			return err
		}
		if status.State == desired {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %s; current state is %s", stateText(desired), stateText(status.State))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func isNotInstalled(err error) bool {
	return errors.Is(err, errServiceDoesNotExist)
}

func isMarkedForDelete(err error) bool {
	return errors.Is(err, errServiceMarkedDelete)
}

func stateText(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start pending"
	case svc.StopPending:
		return "stop pending"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continue pending"
	case svc.PausePending:
		return "pause pending"
	case svc.Paused:
		return "paused"
	default:
		return fmt.Sprintf("unknown(%d)", state)
	}
}
