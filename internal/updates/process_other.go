//go:build !windows

package updates

import (
	"fmt"
	"os/exec"
	"time"
)

func hideWindow(cmd *exec.Cmd) {}
func waitForExit(pid int, timeout time.Duration) error {
	return fmt.Errorf("automatic program installation currently requires Windows")
}
