package updates

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

func hideWindow(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
func waitForExit(pid int, timeout time.Duration) error {
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
