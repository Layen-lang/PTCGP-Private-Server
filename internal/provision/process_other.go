//go:build !windows

package provision

import "os/exec"

func hideWindow(cmd *exec.Cmd) {}
