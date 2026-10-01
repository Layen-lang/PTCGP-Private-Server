//go:build !windows

package updates

import (
	"fmt"
	"os/exec"
	"time"
)

func hideWindow(cmd *exec.Cmd)          {}
func startDetached(cmd *exec.Cmd) error { return cmd.Start() }
func fileBusy(err error) bool           { return false }
func waitForExit(pid int, timeout time.Duration) error {
	return fmt.Errorf("automatic program installation currently requires Windows")
}
