package updates

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func hideWindow(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }

func startDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_BREAKAWAY_FROM_JOB}
	err := cmd.Start()
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		cmd.SysProcAttr.CreationFlags &^= windows.CREATE_BREAKAWAY_FROM_JOB
		return cmd.Start()
	}
	return err
}

func fileBusy(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}

func waitForExit(pid int, timeout time.Duration) error {
	if pid == 0 {
		return nil
	}
	h, e := syscall.OpenProcess(0x00100000, false, uint32(pid))
	if e != nil {
		if e == syscall.Errno(87) {
			return nil
		}
		return e
	}
	defer syscall.CloseHandle(h)
	result, e := syscall.WaitForSingleObject(h, uint32(timeout.Milliseconds()))
	if e != nil {
		return e
	}
	if result != 0 {
		return fmt.Errorf("previous panel has not exited")
	}
	return nil
}
